<!--
  Datasources.vue - 数据源管理 - 在线报表系统
  模板与逻辑均由迁移前的 datasources.html（in-DOM 模板）与内联脚本原样搬入，业务逻辑未改动。
-->
<template>
  <div>
    <div class="page-body">
      <div class="toolbar">
        <el-button v-if="can('datasource:create')" type="primary" @click="openDialog()"><el-icon style="margin-right:4px"><i-plus></i-plus></el-icon>新建数据源</el-button>
      </div>

      <el-table :data="list" v-loading="loading" border stripe>
        <el-table-column prop="id" label="ID" width="64"></el-table-column>
        <el-table-column prop="name" label="名称" min-width="140"></el-table-column>
        <el-table-column prop="dbType" label="类型" width="100">
          <template #default="s"><el-tag size="small">{{ s.row.dbType }}</el-tag></template>
        </el-table-column>
        <el-table-column label="地址" min-width="180">
          <template #default="s">
            <span v-if="s.row.dbType==='sqlite'" class="mono">{{ s.row.serviceName }}</span>
            <span v-else class="mono">{{ s.row.host }}:{{ s.row.port }} / {{ s.row.serviceName }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="username" label="用户" width="110"></el-table-column>
        <el-table-column label="操作" width="280" fixed="right">
          <template #default="s">
            <el-button v-if="can('datasource:test')" size="small" type="primary" link :loading="s.row._testing" @click="testById(s.row)">测试连接</el-button>
            <el-button size="small" type="primary" link @click="browse(s.row)">字段扫描</el-button>
            <el-button v-if="can('datasource:update')" size="small" link @click="openDialog(s.row)">编辑</el-button>
            <el-button v-if="can('datasource:delete')" size="small" type="danger" link @click="remove(s.row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <!-- 新建/编辑 -->
      <el-dialog v-model="dialogVisible" :title="form.id ? '编辑数据源' : '新建数据源'" width="560px">
        <el-form :model="form" label-width="100px">
          <el-form-item label="名称" required><el-input v-model="form.name"></el-input></el-form-item>
          <el-form-item label="数据库类型" required>
            <el-select v-model="form.dbType" style="width:100%">
              <el-option v-for="t in types" :key="t" :label="t" :value="t"></el-option>
            </el-select>
          </el-form-item>
          <el-form-item v-if="form.dbType!=='sqlite'" label="主机"><el-input v-model="form.host"></el-input></el-form-item>
          <el-form-item v-if="form.dbType!=='sqlite'" label="端口"><el-input-number v-model="form.port" :controls="false" style="width:100%"></el-input-number></el-form-item>
          <el-form-item :label="form.dbType==='sqlite' ? '数据库文件路径' : '服务名/库名'">
            <el-input v-model="form.serviceName"></el-input>
          </el-form-item>
          <el-form-item v-if="form.dbType!=='sqlite'" label="用户名"><el-input v-model="form.username"></el-input></el-form-item>
          <el-form-item v-if="form.dbType!=='sqlite'" label="密码">
            <el-input v-model="form.password" type="password" show-password
                      :placeholder="form.id ? '留空表示不修改' : ''"></el-input>
          </el-form-item>
          <el-form-item label="附加连接串"><el-input v-model="form.extraConn" placeholder="如 ?useSSL=false"></el-input></el-form-item>
          <el-form-item label="连接池">
            <el-input-number v-model="form.poolMaxOpen" :min="1" :controls="false" style="width:90px"></el-input-number>
            <span class="muted" style="margin:0 6px">最大 / 空闲</span>
            <el-input-number v-model="form.poolMaxIdle" :min="1" :controls="false" style="width:90px"></el-input-number>
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="testInput" :loading="testingInput">测试连接</el-button>
          <el-button @click="dialogVisible=false">取消</el-button>
          <el-button type="primary" :loading="saving" @click="save">保存</el-button>
        </template>
      </el-dialog>

      <!-- 元数据浏览 -->
      <el-dialog v-model="browseVisible" :title="'字段扫描 - ' + (browseDs ? browseDs.name : '')" width="760px">
        <div style="display:flex;gap:16px">
          <div style="width:260px">
            <el-input v-model="tableFilter" placeholder="搜索表名" clearable size="small" style="margin-bottom:8px"></el-input>
            <div v-loading="tablesLoading" style="max-height:420px;overflow:auto;border:1px solid #ebeef5;border-radius:4px">
              <div v-for="t in filteredTables" :key="t.name"
                   @click="loadColumns(t.name)"
                   :style="{padding:'6px 10px',cursor:'pointer',background: t.name===currentTable ? '#ecf5ff' : '',fontSize:'13px'}"
                   class="mono">{{ t.name }}
                <span class="muted" v-if="t.comment"> {{ t.comment }}</span>
              </div>
              <div v-if="!tablesLoading && !filteredTables.length" class="muted" style="padding:10px">无表</div>
            </div>
          </div>
          <div style="flex:1">
            <div class="muted" style="margin-bottom:6px">{{ currentTable ? ('表 ' + currentTable + ' 的字段（' + columns.length + '）') : '← 点击左侧表名扫描字段' }}</div>
            <el-table :data="columns" v-loading="columnsLoading" border size="small" max-height="420">
              <el-table-column prop="name" label="字段名" min-width="140" class-name="mono"></el-table-column>
              <el-table-column prop="dataType" label="类型" width="130" class-name="mono"></el-table-column>
              <el-table-column prop="nullable" label="可空" width="60">
                <template #default="s">{{ s.row.nullable ? '是' : '否' }}</template>
              </el-table-column>
              <el-table-column prop="comment" label="注释" min-width="120"></el-table-column>
            </el-table>
          </div>
        </div>
      </el-dialog>
    </div>
  </div>
</template>

<script>
import { ElMessageBox, ElMessage } from 'element-plus'
import { RS } from '@/lib/rs.js'

export default {
  data: function () {
    return {
      list: [], types: [], loading: false,
      dialogVisible: false, saving: false, testingInput: false,
      form: this.emptyForm(),
      browseVisible: false, browseDs: null,
      tables: [], tableFilter: '', tablesLoading: false,
      columns: [], currentTable: '', columnsLoading: false
    };
  },
  computed: {
    filteredTables: function () {
      var f = this.tableFilter.toLowerCase();
      return this.tables.filter(function (t) { return !f || t.name.toLowerCase().indexOf(f) >= 0; });
    }
  },
  created: function () {
    var self = this;
    RS.guard('datasource:list').then(function () {
      self.load();
      RS.get('/api/datasource-types').then(function (t) { self.types = t; }).catch(function () {});
    }).catch(function () {});
  },
  methods: {
    emptyForm: function () {
      return { id: 0, name: '', dbType: 'mysql', host: '', port: 3306, serviceName: '',
        username: '', password: '', extraConn: '', poolMaxOpen: 10, poolMaxIdle: 5 };
    },
    load: function () {
      var self = this;
      this.loading = true;
      RS.get('/api/datasources').then(function (l) { self.list = l; })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.loading = false; });
    },
    openDialog: function (row) {
      if (row) {
        this.form = Object.assign(this.emptyForm(), row, { password: '' });
      } else {
        this.form = this.emptyForm();
      }
      this.dialogVisible = true;
    },
    save: function () {
      var self = this;
      var f = this.form;
      if (!f.name || !f.dbType) { ElMessage.warning('名称与类型必填'); return; }
      if (!f.id && f.dbType !== 'sqlite' && !f.password) { ElMessage.warning('密码不能为空'); return; }
      this.saving = true;
      var p = f.id ? RS.put('/api/datasources/' + f.id, f) : RS.post('/api/datasources', f);
      p.then(function () { ElMessage.success('已保存'); self.dialogVisible = false; self.load(); })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.saving = false; });
    },
    testInput: function () {
      var self = this;
      this.testingInput = true;
      RS.post('/api/datasources/test', this.form)
        .then(function (r) { ElMessage.success('连接成功，延迟 ' + r.latencyMs + 'ms，版本 ' + r.version); })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.testingInput = false; });
    },
    testById: function (row) {
      row._testing = true;
      RS.post('/api/datasources/' + row.id + '/test')
        .then(function (r) { ElMessage.success('连接成功，延迟 ' + r.latencyMs + 'ms，版本 ' + r.version); })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { row._testing = false; });
    },
    browse: function (row) {
      this.browseDs = row;
      this.browseVisible = true;
      this.tables = []; this.columns = []; this.currentTable = '';
      var self = this;
      this.tablesLoading = true;
      RS.get('/api/datasources/' + row.id + '/tables')
        .then(function (t) { self.tables = t; })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.tablesLoading = false; });
    },
    loadColumns: function (name) {
      this.currentTable = name;
      this.columnsLoading = true;
      var self = this;
      RS.get('/api/datasources/' + this.browseDs.id + '/tables/' + encodeURIComponent(name) + '/columns')
        .then(function (c) { self.columns = c; })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.columnsLoading = false; });
    },
    remove: function (row) {
      var self = this;
      ElMessageBox.confirm('确定删除数据源「' + row.name + '」？', '删除确认', { type: 'warning' })
        .then(function () { return RS.del('/api/datasources/' + row.id); })
        .then(function () { ElMessage.success('已删除'); self.load(); })
        .catch(function (e) { if (e !== 'cancel' && e !== 'close') RS.alertErr(e); });
    }
  }
}
</script>
