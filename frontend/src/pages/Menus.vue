<!--
  Menus.vue - 菜单管理 - 在线报表系统
  模板与逻辑均由迁移前的 menus.html（in-DOM 模板）与内联脚本原样搬入，业务逻辑未改动。
-->
<template>
  <div>
    <div class="page-body">
      <div class="toolbar">
        <el-button v-if="can('system:menu:create')" type="primary" @click="openDialog()">
          <el-icon style="margin-right:4px"><i-plus></i-plus></el-icon>新建菜单
        </el-button>
        <span class="muted">目录（M）聚合页面，页面（C）对应一个前端路由（如 /reports），按钮（F）为接口与前端按钮共用的权限点</span>
      </div>

      <el-table :data="tree" v-loading="loading" border stripe row-key="id"
                :tree-props="{ children: 'children' }" default-expand-all>
        <el-table-column prop="menuName" label="菜单名称" min-width="200"></el-table-column>
        <el-table-column label="类型" width="90">
          <template #default="s">
            <el-tag size="small" v-if="s.row.menuType==='M'" type="warning">目录</el-tag>
            <el-tag size="small" v-else-if="s.row.menuType==='C'">页面</el-tag>
            <el-tag size="small" v-else type="info">按钮</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="访问路径" min-width="170">
          <template #default="s"><span class="mono">{{ s.row.path || '—' }}</span></template>
        </el-table-column>
        <el-table-column label="权限标识" min-width="180">
          <template #default="s"><span class="mono">{{ s.row.perms || '—' }}</span></template>
        </el-table-column>
        <el-table-column prop="sortNo" label="排序" width="70"></el-table-column>
        <el-table-column label="可见" width="70">
          <template #default="s">
            <el-tag size="small" v-if="s.row.visible===1" type="success">显示</el-tag>
            <el-tag size="small" v-else type="info">隐藏</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="70">
          <template #default="s">
            <el-tag size="small" v-if="s.row.status===1" type="success">启用</el-tag>
            <el-tag size="small" v-else type="danger">停用</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="230" fixed="right">
          <template #default="s">
            <el-button v-if="can('system:menu:create') && s.row.menuType!=='F'" size="small" type="primary" link @click="openDialog(null, s.row)">新增下级</el-button>
            <el-button v-if="can('system:menu:update')" size="small" link @click="openDialog(s.row)">编辑</el-button>
            <el-button v-if="can('system:menu:delete')" size="small" type="danger" link @click="remove(s.row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <el-dialog v-model="dialogVisible" :title="form.id ? '编辑菜单' : '新建菜单'" width="540px">
      <el-form :model="form" label-width="100px">
        <el-form-item label="上级菜单">
          <el-tree-select v-model="form.parentId" :data="parentOptions" check-strictly
                          :render-after-expand="false" node-key="id"
                          :props="{ label: 'menuName', children: 'children' }" style="width:100%"></el-tree-select>
        </el-form-item>
        <el-form-item label="类型" required>
          <el-radio-group v-model="form.menuType">
            <el-radio-button value="M">目录</el-radio-button>
            <el-radio-button value="C">页面</el-radio-button>
            <el-radio-button value="F">按钮</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="名称" required>
          <el-input v-model="form.menuName" placeholder="导航/按钮显示名"></el-input>
        </el-form-item>
        <el-form-item v-if="form.menuType==='C'" label="访问路径" required>
          <el-input v-model="form.path" class="mono" placeholder="/reports"></el-input>
        </el-form-item>
        <el-form-item v-if="form.menuType!=='M'" label="权限标识">
          <el-input v-model="form.perms" class="mono" placeholder="如 report:create"></el-input>
          <div class="muted">页面节点填页面级权限（如 system:user:list），按钮节点填操作权限点</div>
        </el-form-item>
        <el-form-item label="排序">
          <el-input-number v-model="form.sortNo" :min="0" :controls="false"></el-input-number>
        </el-form-item>
        <el-form-item label="可见">
          <el-switch v-model="form.visible" :active-value="1" :inactive-value="0" active-text="显示" inactive-text="隐藏"></el-switch>
        </el-form-item>
        <el-form-item label="状态">
          <el-switch v-model="form.status" :active-value="1" :inactive-value="0" active-text="启用" inactive-text="停用"></el-switch>
          <div class="muted">停用等同于收回该节点声明的权限点</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible=false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
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
      tree: [], loading: false, saving: false, dialogVisible: false,
      form: { id: 0, parentId: 0, menuName: '', menuType: 'C', path: '', perms: '', icon: '', sortNo: 0, visible: 1, status: 1 }
    };
  },
  computed: {
    /** 上级下拉：加一个「顶级菜单」根节点，便于把菜单提到第一层 */
    parentOptions: function () {
      return [{ id: 0, menuName: '顶级菜单', children: this.tree }];
    }
  },
  created: function () {
    var self = this;
    RS.guard('system:menu:list').then(function () { self.load(); }).catch(function () {});
  },
  methods: {
    load: function () {
      var self = this;
      this.loading = true;
      RS.get('/api/system/menus')
        .then(function (d) { self.tree = d || []; })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.loading = false; });
    },
    openDialog: function (row, parent) {
      if (row) {
        this.form = {
          id: row.id, parentId: row.parentId || 0, menuName: row.menuName, menuType: row.menuType,
          path: row.path || '', perms: row.perms || '', icon: row.icon || '',
          sortNo: row.sortNo || 0, visible: row.visible, status: row.status
        };
      } else {
        this.form = {
          id: 0, parentId: parent ? parent.id : 0, menuName: '', menuType: parent ? (parent.menuType === 'M' ? 'C' : 'F') : 'C',
          path: '', perms: '', icon: '', sortNo: 0, visible: 1, status: 1
        };
      }
      this.dialogVisible = true;
    },
    save: function () {
      var self = this, f = this.form;
      if (!f.menuName) { ElMessage.warning('请填写名称'); return; }
      if (f.menuType === 'C' && !f.path) { ElMessage.warning('页面菜单需填写访问路径'); return; }
      if (f.menuType === 'F' && !f.perms) { ElMessage.warning('按钮需填写权限标识'); return; }
      this.saving = true;
      var body = {
        parentId: f.parentId || 0, menuName: f.menuName, menuType: f.menuType,
        path: f.path, perms: f.perms, icon: f.icon, sortNo: f.sortNo,
        visible: f.visible, status: f.status
      };
      var req = f.id ? RS.put('/api/system/menus/' + f.id, body) : RS.post('/api/system/menus', body);
      req.then(function () {
        ElMessage.success('保存成功，导航已刷新');
        self.dialogVisible = false;
        self.load();
        RS.refreshMe().catch(function () {});
      }).catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.saving = false; });
    },
    remove: function (row) {
      var self = this;
      ElMessageBox.confirm('确定删除「' + row.menuName + '」？存在下级时需先删除下级', '删除确认', { type: 'warning' })
        .then(function () { return RS.del('/api/system/menus/' + row.id); })
        .then(function () {
          ElMessage.success('已删除');
          self.load();
          RS.refreshMe().catch(function () {});
        })
        .catch(function (e) { if (e !== 'cancel' && e !== 'close') RS.alertErr(e); });
    }
  }
}
</script>
