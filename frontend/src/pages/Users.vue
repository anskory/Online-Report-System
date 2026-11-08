<!--
  Users.vue - 用户管理 - 在线报表系统
  模板与逻辑均由迁移前的 users.html（in-DOM 模板）与内联脚本原样搬入，业务逻辑未改动。
-->
<template>
  <div>
    <div class="page-body">
      <div class="toolbar">
        <el-button v-if="can('system:user:create')" type="primary" @click="openDialog()">
          <el-icon style="margin-right:4px"><i-plus></i-plus></el-icon>新建用户
        </el-button>
        <el-input v-model="keyword" placeholder="按用户名/显示名搜索" clearable style="width:240px"
                  @keyup.enter="load" @clear="load"></el-input>
        <el-button @click="load">查询</el-button>
        <span class="muted">一个账号可挂多个角色，权限取并集</span>
      </div>

      <el-table :data="list" v-loading="loading" border stripe>
        <el-table-column prop="id" label="ID" width="70"></el-table-column>
        <el-table-column prop="username" label="用户名" width="150"></el-table-column>
        <el-table-column prop="displayName" label="显示名" width="150"></el-table-column>
        <el-table-column label="角色" min-width="220">
          <template #default="s">
            <el-tag v-for="r in s.row.roles" :key="r.roleKey" size="small" style="margin-right:6px"
                    :type="r.roleKey==='admin' ? 'danger' : 'info'">{{ r.roleName }}</el-tag>
            <span v-if="!s.row.roles || !s.row.roles.length" class="muted">未分配</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="s">
            <el-tag v-if="s.row.status===1" type="success" size="small">启用</el-tag>
            <el-tag v-else type="info" size="small">禁用</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="最后登录" width="170">
          <template #default="s">{{ fmtTime(s.row.lastLoginAt) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="260" fixed="right">
          <template #default="s">
            <el-button v-if="can('system:user:update')" size="small" type="primary" link @click="openDialog(s.row)">编辑</el-button>
            <el-button v-if="can('system:user:assignRole')" size="small" link @click="openRoleDialog(s.row)">分配角色</el-button>
            <el-button v-if="can('system:user:resetPwd')" size="small" type="warning" link @click="openPwd(s.row)">重置密码</el-button>
            <el-button v-if="can('system:user:delete')" size="small" type="danger" link @click="remove(s.row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <!-- 新建 / 编辑 -->
    <el-dialog v-model="dialogVisible" :title="form.id ? '编辑用户' : '新建用户'" width="480px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="用户名" required>
          <el-input v-model="form.username" :disabled="!!form.id" placeholder="登录账号，创建后不可修改"></el-input>
        </el-form-item>
        <el-form-item label="显示名">
          <el-input v-model="form.displayName" placeholder="用于界面展示"></el-input>
        </el-form-item>
        <el-form-item v-if="!form.id" label="密码" required>
          <el-input v-model="form.password" type="password" show-password placeholder="不少于 6 位"></el-input>
        </el-form-item>
        <el-form-item label="角色" required>
          <el-select v-model="form.roleKeys" multiple style="width:100%" placeholder="请选择角色"
                     :disabled="!!form.id && !can('system:user:assignRole')">
            <el-option v-for="r in roles" :key="r.roleKey" :label="r.roleName + '（' + r.roleKey + '）'" :value="r.roleKey"></el-option>
          </el-select>
          <div class="muted">未分配角色将无法登录；编辑时需「分配角色」权限才能调整</div>
        </el-form-item>
        <el-form-item label="状态">
          <el-switch v-model="form.status" :active-value="1" :inactive-value="0" active-text="启用" inactive-text="禁用"></el-switch>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible=false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>

    <!-- 分配角色 -->
    <el-dialog v-model="roleVisible" title="分配角色" width="440px">
      <el-form label-width="90px">
        <el-form-item label="用户">
          <span class="mono">{{ roleForm.username }}</span>
        </el-form-item>
        <el-form-item label="角色" required>
          <el-select v-model="roleForm.roleKeys" multiple style="width:100%">
            <el-option v-for="r in roles" :key="r.roleKey" :label="r.roleName + '（' + r.roleKey + '）'" :value="r.roleKey"></el-option>
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="roleVisible=false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveRoles">保存</el-button>
      </template>
    </el-dialog>

    <!-- 重置密码 -->
    <el-dialog v-model="pwdVisible" title="重置密码" width="420px">
      <el-form label-width="90px">
        <el-form-item label="用户">
          <span class="mono">{{ pwdForm.username }}</span>
        </el-form-item>
        <el-form-item label="新密码" required>
          <el-input v-model="pwdForm.password" type="password" show-password placeholder="不少于 6 位"></el-input>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="pwdVisible=false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="savePwd">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script>
import { ElMessageBox, ElMessage } from 'element-plus'
import { RS } from '@/lib/rs.js'

export default {
  data: function () {
    return {
      list: [], roles: [], keyword: '', loading: false, saving: false,
      dialogVisible: false,
      form: { id: 0, username: '', displayName: '', password: '', roleKeys: [], status: 1 },
      roleVisible: false, roleForm: { id: 0, username: '', roleKeys: [] },
      pwdVisible: false, pwdForm: { id: 0, username: '', password: '' }
    };
  },
  created: function () {
    var self = this;
    RS.guard('system:user:list').then(function () {
      self.loadRoles();
      self.load();
    }).catch(function () {});
  },
  methods: {
    loadRoles: function () {
      var self = this;
      RS.get('/api/roles').then(function (rs) { self.roles = rs || []; }).catch(function (e) { RS.alertErr(e); });
    },
    load: function () {
      var self = this;
      this.loading = true;
      RS.get('/api/system/users?keyword=' + encodeURIComponent(this.keyword || ''))
        .then(function (d) { self.list = d || []; })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.loading = false; });
    },
    fmtTime: function (t) {
      if (!t) return '—';
      var d = new Date(t);
      if (isNaN(d.getTime()) || d.getFullYear() < 2000) return '—';
      function p(n) { return n < 10 ? '0' + n : '' + n; }
      return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) + ' ' + p(d.getHours()) + ':' + p(d.getMinutes());
    },
    openDialog: function (row) {
      if (row) {
        this.form = {
          id: row.id, username: row.username, displayName: row.displayName, password: '',
          roleKeys: (row.roles || []).map(function (r) { return r.roleKey; }),
          status: row.status
        };
      } else {
        this.form = { id: 0, username: '', displayName: '', password: '', roleKeys: [], status: 1 };
      }
      this.dialogVisible = true;
    },
    save: function () {
      var self = this, f = this.form;
      if (!f.username) { ElMessage.warning('请填写用户名'); return; }
      if (!f.id && (!f.password || f.password.length < 6)) { ElMessage.warning('密码不少于 6 位'); return; }
      if (!f.roleKeys || !f.roleKeys.length) { ElMessage.warning('请至少选择一个角色'); return; }
      this.saving = true;
      var req = f.id
        ? RS.put('/api/system/users/' + f.id, { displayName: f.displayName, status: f.status })
            .then(function () {
              // 角色单独接口，避免误改；无分配角色权限时不发请求
              if (RS.can('system:user:assignRole')) {
                return RS.put('/api/system/users/' + f.id + '/roles', { roleKeys: f.roleKeys });
              }
            })
        : RS.post('/api/system/users', {
            username: f.username, password: f.password, displayName: f.displayName,
            roleKeys: f.roleKeys, status: f.status
          });
      req.then(function () {
        ElMessage.success('保存成功');
        self.dialogVisible = false;
        self.load();
      }).catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.saving = false; });
    },
    openRoleDialog: function (row) {
      this.roleForm = {
        id: row.id, username: row.username,
        roleKeys: (row.roles || []).map(function (r) { return r.roleKey; })
      };
      this.roleVisible = true;
    },
    saveRoles: function () {
      var self = this, f = this.roleForm;
      if (!f.roleKeys || !f.roleKeys.length) { ElMessage.warning('请至少选择一个角色'); return; }
      this.saving = true;
      RS.put('/api/system/users/' + f.id + '/roles', { roleKeys: f.roleKeys })
        .then(function () {
          ElMessage.success('已保存');
          self.roleVisible = false;
          self.load();
        })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.saving = false; });
    },
    openPwd: function (row) {
      this.pwdForm = { id: row.id, username: row.username, password: '' };
      this.pwdVisible = true;
    },
    savePwd: function () {
      var self = this, f = this.pwdForm;
      if (!f.password || f.password.length < 6) { ElMessage.warning('密码不少于 6 位'); return; }
      this.saving = true;
      RS.post('/api/system/users/' + f.id + '/reset-password', { password: f.password })
        .then(function () {
          ElMessage.success('密码已重置');
          self.pwdVisible = false;
        })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.saving = false; });
    },
    remove: function (row) {
      var self = this;
      ElMessageBox.confirm('确定删除用户「' + row.username + '」？', '删除确认', { type: 'warning' })
        .then(function () { return RS.del('/api/system/users/' + row.id); })
        .then(function () { ElMessage.success('已删除'); self.load(); })
        .catch(function (e) { if (e !== 'cancel' && e !== 'close') RS.alertErr(e); });
    }
  }
}
</script>
