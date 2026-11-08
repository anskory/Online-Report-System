<!--
  Reports.vue - 报表列表 - 在线报表系统
  模板与逻辑均由迁移前的 reports.html（in-DOM 模板）与内联脚本原样搬入，业务逻辑未改动。
-->
<template>
  <!-- rs-page-fill：页面占满内容区，剩余高度给表格，列表内部滚动 -->
  <div class="rs-page-fill">
    <div class="page-body">
      <div class="page-head">
        <h3>报表中心</h3>
        <div class="desc">共 {{ filtered.length }} 张报表 · {{ groups.length }} 个分组</div>
      </div>
      <div class="toolbar">
        <el-select v-model="groupFilter" placeholder="全部分组" clearable style="width:180px">
          <el-option v-for="g in groups" :key="g.id" :label="g.name" :value="g.id"></el-option>
        </el-select>
        <el-input v-model="keyword" placeholder="按名称搜索" clearable style="width:220px"></el-input>
        <el-button v-if="can('report:create')" style="margin-left:auto" @click="openWizard">
          <el-icon style="margin-right:3px"><i-magic-stick></i-magic-stick></el-icon>从表建报表
        </el-button>
        <!-- 报表配置入口：不再依赖左侧菜单，统一从报表中心进（进去后可从顶栏返回） -->
        <el-button v-if="can('report:create')" type="primary" @click="createReport">
          <el-icon style="margin-right:3px"><i-plus></i-plus></el-icon>报表配置
        </el-button>
      </div>

      <!-- 表格撑满剩余高度：表头固定，行数据在表格内部滚动 -->
      <div class="table-wrap" ref="tableWrap">
        <el-table :data="filtered" v-loading="loading" border stripe :height="tableHeight">
          <el-table-column prop="id" label="ID" width="64"></el-table-column>
          <el-table-column prop="name" label="报表名称" min-width="160"></el-table-column>
          <el-table-column label="分组" width="120">
            <template #default="s">{{ groupName(s.row.groupId) }}</template>
          </el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="s">
              <el-tag v-if="s.row.status==='published'" type="success">已发布</el-tag>
              <el-tag v-else-if="s.row.status==='disabled'" type="info">已停用</el-tag>
              <el-tag v-else type="warning">草稿</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="公开访问" width="90">
            <template #default="s">
              <el-tag v-if="s.row.isPublic===1" type="danger">开启</el-tag>
              <span v-else class="muted">—</span>
            </template>
          </el-table-column>
          <el-table-column prop="remark" label="备注" min-width="140"></el-table-column>
          <el-table-column label="操作" width="260" fixed="right">
            <template #default="s">
              <el-button size="small" type="primary" link :disabled="s.row.status!=='published'" @click="view(s.row)">查看</el-button>
              <el-button v-if="can('report:update')" size="small" type="primary" link @click="edit(s.row)">配置</el-button>
              <template v-if="can('report:publish')">
                <el-button v-if="s.row.status!=='published'" size="small" type="success" link @click="setStatus(s.row,'published')">发布</el-button>
                <el-button v-else size="small" type="warning" link @click="setStatus(s.row,'disabled')">停用</el-button>
              </template>
              <el-button v-if="can('report:delete')" size="small" type="danger" link @click="remove(s.row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </div>
    </div>

    <!-- 从表建报表：三步向导（选表 → 勾字段 → 预览并创建草稿） -->
    <report-wizard ref="wizard" @created="onWizardCreated"></report-wizard>
  </div>
</template>

<script>
import { ElMessageBox, ElMessage } from 'element-plus'
import { RS } from '@/lib/rs.js'
import tableFill from '@/lib/tableFill.js'
import ReportWizard from '@/components/ReportWizard.vue'

export default {
  mixins: [tableFill],
  components: { ReportWizard },
  data: function () {
    return {
      user: RS.user(),
      list: [], groups: [],
      groupFilter: null, keyword: '',
      loading: false
    };
  },
  computed: {
    roleText: function () {
      var roles = RS.me.roles || [];
      if (roles.length) return roles.map(function (r) { return r.roleName; }).join('、');
      return this.user.role === 'admin' ? '管理员' : '普通用户';
    },
    filtered: function () {
      var self = this;
      return this.list.filter(function (r) {
        if (self.groupFilter && r.groupId !== self.groupFilter) return false;
        if (self.keyword && r.name.indexOf(self.keyword) < 0) return false;
        return true;
      });
    }
  },
  created: function () {
    var self = this;
    RS.guard('report:view').then(function () { self.load(); }).catch(function () {});
  },
  methods: {
    load: function () {
      var self = this;
      this.loading = true;
      Promise.all([RS.get('/api/reports'), RS.get('/api/report-groups')])
        .then(function (rs) { self.list = rs[0]; self.groups = rs[1]; })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.loading = false; });
    },
    groupName: function (id) {
      var g = this.groups.find(function (x) { return x.id === id; });
      return g ? g.name : id;
    },
    view: function (r) { this.$router.push({ path: '/view', query: { id: r.id } }); },
    /** 进入报表配置（新建）：配置页顶部有「返回报表中心」 */
    createReport: function () { this.$router.push('/config').catch(function () {}); },
    /** 打开「从表建报表」向导 */
    openWizard: function () {
      var w = this.$refs.wizard;
      if (w) w.open();
    },
    /** 向导创建成功：刷新列表并直接进入配置页继续精调 */
    onWizardCreated: function (id) {
      this.load();
      if (id) this.$router.push({ path: '/config', query: { id: id } });
    },
    edit: function (r) { this.$router.push({ path: '/config', query: { id: r.id } }); },
    setStatus: function (r, status) {
      var self = this;
      RS.post('/api/reports/' + r.id + '/publish', { status: status })
        .then(function () { ElMessage.success('操作成功'); self.load(); })
        .catch(function (e) { RS.alertErr(e); });
    },
    remove: function (r) {
      var self = this;
      ElMessageBox.confirm('确定删除报表「' + r.name + '」？相关条件与列配置将一并删除', '删除确认', { type: 'warning' })
        .then(function () { return RS.del('/api/reports/' + r.id); })
        .then(function () { ElMessage.success('已删除'); self.load(); })
        .catch(function (e) { if (e !== 'cancel' && e !== 'close') RS.alertErr(e); });
    }
  }
}
</script>
