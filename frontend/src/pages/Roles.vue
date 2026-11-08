<!--
  Roles.vue - 角色管理 - 在线报表系统
  模板与逻辑均由迁移前的 roles.html（in-DOM 模板）与内联脚本原样搬入，业务逻辑未改动。
-->
<template>
  <div>
    <div class="page-body">
      <div class="toolbar">
        <el-button v-if="can('system:role:create')" type="primary" @click="openDialog()">
          <el-icon style="margin-right:4px"><i-plus></i-plus></el-icon>新建角色
        </el-button>
        <span class="muted">角色的权限 = 勾选的菜单/权限点 ＋ 可见报表；用户的权限为多个角色并集</span>
      </div>

      <el-table :data="list" v-loading="loading" border stripe>
        <el-table-column prop="id" label="ID" width="70"></el-table-column>
        <el-table-column prop="roleName" label="角色名称" width="170"></el-table-column>
        <el-table-column prop="roleKey" label="角色标识" width="150">
          <template #default="s"><span class="mono">{{ s.row.roleKey }}</span></template>
        </el-table-column>
        <el-table-column prop="sortNo" label="排序" width="80"></el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="s">
            <el-tag v-if="s.row.status===1" type="success" size="small">启用</el-tag>
            <el-tag v-else type="info" size="small">停用</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="类型" width="90">
          <template #default="s">
            <el-tag v-if="s.row.builtin===1" type="warning" size="small">内置</el-tag>
            <span v-else class="muted">自定义</span>
          </template>
        </el-table-column>
        <el-table-column prop="remark" label="备注" min-width="180"></el-table-column>
        <el-table-column label="操作" width="300" fixed="right">
          <template #default="s">
            <el-button v-if="can('system:role:assignMenu')" size="small" type="primary" link @click="openMenu(s.row)">菜单权限</el-button>
            <el-button v-if="can('system:role:assignReport')" size="small" type="success" link @click="openReport(s.row)">报表授权</el-button>
            <el-button v-if="can('system:role:update')" size="small" link @click="openDialog(s.row)">编辑</el-button>
            <el-button v-if="can('system:role:delete') && s.row.builtin!==1" size="small" type="danger" link @click="remove(s.row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <!-- 新建 / 编辑角色 -->
    <el-dialog v-model="dialogVisible" :title="form.id ? '编辑角色' : '新建角色'" width="480px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="角色标识" required>
          <el-input v-model="form.roleKey" :disabled="!!form.id" placeholder="小写字母开头，如 report_dev"></el-input>
          <div class="muted">标识是权限关联的唯一依据，创建后不可修改</div>
        </el-form-item>
        <el-form-item label="角色名称" required>
          <el-input v-model="form.roleName" placeholder="如 报表开发"></el-input>
        </el-form-item>
        <el-form-item label="排序">
          <el-input-number v-model="form.sortNo" :min="0" :controls="false"></el-input-number>
        </el-form-item>
        <el-form-item label="状态">
          <el-switch v-model="form.status" :active-value="1" :inactive-value="0" active-text="启用" inactive-text="停用"></el-switch>
          <div class="muted">停用后该角色的权限立即失效（用户其它角色不受影响）</div>
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="form.remark" type="textarea" :rows="2"></el-input>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible=false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>

    <!-- 菜单权限 -->
    <el-drawer v-model="menuVisible" :title="'菜单权限 - ' + (current.roleName || '')" size="420px" destroy-on-close>
      <div style="margin-bottom:10px" class="muted">勾选页面与按钮权限点；目录会随子项自动半选保存</div>
      <el-tree ref="menuTree" :data="menuTree" show-checkbox node-key="id" default-expand-all
               :props="{ label: 'menuName', children: 'children' }"
               :default-checked-keys="menuChecked"></el-tree>
      <div style="margin-top:16px;text-align:right">
        <el-button @click="menuVisible=false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveMenu">保存</el-button>
      </div>
    </el-drawer>

    <!-- 报表授权 -->
    <el-drawer v-model="reportVisible" :title="'报表授权 - ' + (current.roleName || '')" size="460px" destroy-on-close>
      <div class="toolbar">
        <el-button size="small" @click="checkAllReports(true)">全选</el-button>
        <el-button size="small" @click="checkAllReports(false)">清空</el-button>
        <span class="muted">未勾选的报表该角色不可见（管理员不受限）</span>
      </div>
      <div v-if="!reportList.length" class="muted">暂无报表</div>
      <el-checkbox-group v-model="reportChecked" style="display:flex;flex-direction:column;gap:6px">
        <el-checkbox v-for="r in reportList" :key="r.id" :value="r.id">
          {{ r.name }}<span class="muted" style="margin-left:8px">{{ r.status === 'published' ? '' : '（' + r.status + '）' }}</span>
        </el-checkbox>
      </el-checkbox-group>
      <div style="margin-top:16px;text-align:right">
        <el-button @click="reportVisible=false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveReport">保存</el-button>
      </div>
    </el-drawer>
  </div>
</template>

<script>
import { ElMessageBox, ElMessage } from 'element-plus'
import { RS } from '@/lib/rs.js'

export default {
  data: function () {
    return {
      list: [], loading: false, saving: false,
      dialogVisible: false,
      form: { id: 0, roleKey: '', roleName: '', sortNo: 0, status: 1, remark: '' },
      current: {},
      menuVisible: false, menuTree: [], menuChecked: [],
      reportVisible: false, reportList: [], reportChecked: []
    };
  },
  created: function () {
    var self = this;
    RS.guard('system:role:list').then(function () { self.load(); }).catch(function () {});
  },
  methods: {
    load: function () {
      var self = this;
      this.loading = true;
      RS.get('/api/system/roles')
        .then(function (d) { self.list = d || []; })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.loading = false; });
    },
    openDialog: function (row) {
      this.form = row
        ? { id: row.id, roleKey: row.roleKey, roleName: row.roleName, sortNo: row.sortNo, status: row.status, remark: row.remark }
        : { id: 0, roleKey: '', roleName: '', sortNo: 0, status: 1, remark: '' };
      this.dialogVisible = true;
    },
    save: function () {
      var self = this, f = this.form;
      if (!f.id && !/^[a-z][a-z0-9_]{1,31}$/.test(f.roleKey || '')) {
        ElMessage.warning('角色标识需小写字母开头，2-32 位字母/数字/下划线');
        return;
      }
      if (!f.roleName) { ElMessage.warning('请填写角色名称'); return; }
      this.saving = true;
      var body = { roleKey: f.roleKey, roleName: f.roleName, sortNo: f.sortNo, status: f.status, remark: f.remark };
      var req = f.id ? RS.put('/api/system/roles/' + f.id, body) : RS.post('/api/system/roles', body);
      req.then(function () {
        ElMessage.success('保存成功');
        self.dialogVisible = false;
        self.load();
      }).catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.saving = false; });
    },
    remove: function (row) {
      var self = this;
      ElMessageBox.confirm('确定删除角色「' + row.roleName + '」？已授权的菜单/报表关系会一并清理', '删除确认', { type: 'warning' })
        .then(function () { return RS.del('/api/system/roles/' + row.id); })
        .then(function () { ElMessage.success('已删除'); self.load(); })
        .catch(function (e) { if (e !== 'cancel' && e !== 'close') RS.alertErr(e); });
    },

    /** 菜单授权：树上的父节点只做半选，回显时只传叶子避免父节点把子节点全部带选 */
    openMenu: function (row) {
      var self = this;
      this.current = row;
      Promise.all([RS.get('/api/system/menus'), RS.get('/api/system/roles/' + row.id + '/menus')])
        .then(function (rs) {
          self.menuTree = rs[0] || [];
          self.menuChecked = self.leafChecked(rs[1] || []);
          self.menuVisible = true;
        })
        .catch(function (e) { RS.alertErr(e); });
    },
    leafChecked: function (ids) {
      var set = {}, out = [];
      (ids || []).forEach(function (i) { set[i] = 1; });
      (function walk(nodes) {
        (nodes || []).forEach(function (n) {
          if (n.children && n.children.length) walk(n.children);
          else if (set[n.id]) out.push(n.id);
        });
      })(this.menuTree);
      return out;
    },
    saveMenu: function () {
      var self = this;
      var tree = this.$refs.menuTree;
      if (!tree) return;
      var ids = tree.getCheckedKeys().concat(tree.getHalfCheckedKeys());
      this.saving = true;
      RS.put('/api/system/roles/' + this.current.id + '/menus', { menuIds: ids })
        .then(function () {
          ElMessage.success('权限已保存，相关用户下次请求即生效');
          self.menuVisible = false;
          // 若改的是自己所属角色，导航与按钮权限立即刷新
          RS.refreshMe().catch(function () {});
        })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.saving = false; });
    },

    /** 报表授权：与报表配置页「可见角色」写同一张 rpt_role_report */
    openReport: function (row) {
      var self = this;
      this.current = row;
      Promise.all([RS.get('/api/reports'), RS.get('/api/system/roles/' + row.id + '/reports')])
        .then(function (rs) {
          self.reportList = rs[0] || [];
          self.reportChecked = rs[1] || [];
          self.reportVisible = true;
        })
        .catch(function (e) { RS.alertErr(e); });
    },
    checkAllReports: function (all) {
      this.reportChecked = all ? this.reportList.map(function (r) { return r.id; }) : [];
    },
    saveReport: function () {
      var self = this;
      this.saving = true;
      RS.put('/api/system/roles/' + this.current.id + '/reports', { reportIds: this.reportChecked })
        .then(function () {
          ElMessage.success('报表授权已保存');
          self.reportVisible = false;
        })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.saving = false; });
    }
  }
}
</script>
