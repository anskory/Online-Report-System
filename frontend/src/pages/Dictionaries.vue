<!--
  Dictionaries.vue - 字典管理 - 在线报表系统
  模板与逻辑均由迁移前的 dictionaries.html（in-DOM 模板）与内联脚本原样搬入，业务逻辑未改动。
-->
<template>
  <div>
    <div class="page-body" v-loading="loading">
      <div class="toolbar">
        <el-button v-if="can('dict:create')" type="primary" @click="openDialog()"><el-icon style="margin-right:4px"><i-plus></i-plus></el-icon>新建字典</el-button>
        <span class="muted">字典 = 可复用的键值标签集（按字典类型分组）。报表的查询条件选项与展示列翻译均可引用</span>
      </div>

      <el-table :data="list" border stripe v-loading="loading">
        <el-table-column prop="id" label="ID" width="64"></el-table-column>
        <el-table-column prop="code" label="编码" width="150">
          <template #default="s"><span class="mono">{{ s.row.code }}</span></template>
        </el-table-column>
        <el-table-column prop="name" label="字典名称" width="160"></el-table-column>
        <el-table-column label="来源类型" width="100">
          <template #default="s">
            <el-tag size="small" :type="{separated:'warning',aggregate:'success',sql:'primary'}[s.row.sourceType]">
              {{ {separated:'分离式',aggregate:'聚合式',sql:'SQL式'}[s.row.sourceType] }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="数据定义" min-width="320">
          <template #default="s">
            <span v-if="s.row.sourceType==='separated'" class="mono">
              类型表 {{ s.row.typeTable }}.{{ s.row.typeCodeCol }}<template v-if="s.row.typeLabelCol">（名称列 {{ s.row.typeLabelCol }}）</template> ⇄ 数据表 {{ s.row.dataTable }}（{{ s.row.dataFKCol }} → {{ s.row.typeCodeCol }}）｜{{ s.row.valueCol }} / {{ s.row.labelCol }}
            </span>
            <span v-else-if="s.row.sourceType==='aggregate'" class="mono">
              {{ s.row.tableName }}：类型列 {{ s.row.typeCol }}<template v-if="s.row.typeLabelCol">（名称列 {{ s.row.typeLabelCol }}）</template>｜{{ s.row.valueCol }} / {{ s.row.labelCol }}<template v-if="s.row.filterWhere"> WHERE {{ s.row.filterWhere }}</template>
            </span>
            <span v-else class="mono">SQL 映射：{{ s.row.typeCol }} / {{ s.row.valueCol }} / {{ s.row.labelCol }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="80">
          <template #default="s"><el-tag size="small" :type="s.row.status===1?'success':'info'">{{ s.row.status===1?'启用':'停用' }}</el-tag></template>
        </el-table-column>
        <el-table-column label="操作" width="180" fixed="right">
          <template #default="s">
            <el-button v-if="can('dict:update')" size="small" type="primary" link @click="openDialog(s.row)">编辑</el-button>
            <el-button size="small" type="success" link @click="preview(s.row)">试跑</el-button>
            <el-button v-if="can('dict:delete')" size="small" type="danger" link @click="remove(s.row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <!-- 编辑对话框 -->
      <el-dialog v-model="dlgVisible" :title="form.id ? '编辑字典' : '新建字典'" width="680px" top="5vh">
        <el-form label-width="120px">
          <el-form-item label="字典编码" required>
            <el-input v-model="form.code" class="mono" style="width:220px" :disabled="!!form.id" placeholder="如 MAT_STATUS"></el-input>
            <span class="muted" style="margin-left:8px">报表按编码引用，创建后不可改</span>
          </el-form-item>
          <el-form-item label="字典名称" required>
            <el-input v-model="form.name" style="width:220px" placeholder="如 物料状态"></el-input>
          </el-form-item>
          <el-form-item label="数据源" required>
            <el-select v-model="form.datasourceId" style="width:300px" placeholder="字典表/查询所在的数据源" @change="onDsChange">
              <el-option v-for="d in datasources" :key="d.id" :label="d.name + '（' + d.dbType + '）'" :value="d.id"></el-option>
            </el-select>
          </el-form-item>
          <el-form-item label="来源类型" required>
            <el-radio-group v-model="form.sourceType">
              <el-radio label="separated">分离式（类型表 + 数据表）</el-radio>
              <el-radio label="aggregate">聚合式（单表）</el-radio>
              <el-radio label="sql">SQL式</el-radio>
            </el-radio-group>
          </el-form-item>

          <!-- 分离式 -->
          <template v-if="form.sourceType==='separated'">
            <el-alert type="info" :closable="false" show-icon style="margin-bottom:12px"
                      title="分离式：字典类型存于类型表，键值/标签存于数据表，两表通过关联字段（数据表外键列 = 类型表类型编码列）关联"></el-alert>
            <el-form-item label="类型表" required>
              <el-select v-model="form.typeTable" class="mono" style="width:260px"
                         filterable allow-create default-first-option
                         :disabled="!form.datasourceId" :loading="tablesLoading"
                         placeholder="选择表或手动填写"
                         @visible-change="onTablesVisible" @change="onTypeTableChange">
                <el-option v-for="t in tableOptions" :key="t.name" :label="t.name" :value="t.name">
                  <span>{{ t.name }}</span><span class="muted" style="margin-left:8px">{{ t.comment || t.tableType }}</span>
                </el-option>
              </el-select>
            </el-form-item>
            <el-form-item label="类型编码列" required>
              <el-select v-model="form.typeCodeCol" class="mono" style="width:260px"
                         filterable allow-create default-first-option
                         :disabled="!form.typeTable" :loading="typeColsLoading"
                         placeholder="选择列或手动填写"
                         @visible-change="onTypeColsVisible">
                <el-option v-for="c in typeCols" :key="c.name" :label="c.name" :value="c.name">
                  <span>{{ c.name }}</span><span class="muted" style="margin-left:8px">{{ c.comment || c.dataType }}</span>
                </el-option>
              </el-select>
            </el-form-item>
            <el-form-item label="类型名称列">
              <el-select v-model="form.typeLabelCol" class="mono" style="width:260px"
                         filterable allow-create default-first-option
                         :disabled="!form.typeTable" :loading="typeColsLoading"
                         placeholder="选择列或手动填写"
                         @visible-change="onTypeColsVisible">
                <el-option v-for="c in typeCols" :key="c.name" :label="c.name" :value="c.name">
                  <span>{{ c.name }}</span><span class="muted" style="margin-left:8px">{{ c.comment || c.dataType }}</span>
                </el-option>
              </el-select>
              <span class="muted" style="margin-left:8px">可选，配置后报表里类型下拉显示 名称（编码）</span>
            </el-form-item>
            <el-form-item label="数据表" required>
              <el-select v-model="form.dataTable" class="mono" style="width:260px"
                         filterable allow-create default-first-option
                         :disabled="!form.datasourceId" :loading="tablesLoading"
                         placeholder="选择表或手动填写"
                         @visible-change="onTablesVisible" @change="onDataTableChange">
                <el-option v-for="t in tableOptions" :key="t.name" :label="t.name" :value="t.name">
                  <span>{{ t.name }}</span><span class="muted" style="margin-left:8px">{{ t.comment || t.tableType }}</span>
                </el-option>
              </el-select>
            </el-form-item>
            <el-form-item label="关联字段" required>
              <el-select v-model="form.dataFKCol" class="mono" style="width:260px"
                         filterable allow-create default-first-option
                         :disabled="!form.dataTable" :loading="dataColsLoading"
                         placeholder="选择列或手动填写"
                         @visible-change="onDataColsVisible">
                <el-option v-for="c in dataCols" :key="c.name" :label="c.name" :value="c.name">
                  <span>{{ c.name }}</span><span class="muted" style="margin-left:8px">{{ c.comment || c.dataType }}</span>
                </el-option>
              </el-select>
              <span class="muted" style="margin-left:8px">数据表外键列（对应类型表的类型编码列）</span>
            </el-form-item>
          </template>

          <!-- 聚合式 -->
          <template v-if="form.sourceType==='aggregate'">
            <el-alert type="info" :closable="false" show-icon style="margin-bottom:12px"
                      title="聚合式：同一张表中既有字典类型列又有键值/标签列，按类型列的值区分不同字典"></el-alert>
            <el-form-item label="表名" required>
              <el-select v-model="form.tableName" class="mono" style="width:260px"
                         filterable allow-create default-first-option
                         :disabled="!form.datasourceId" :loading="tablesLoading"
                         placeholder="选择表或手动填写"
                         @visible-change="onTablesVisible" @change="onAggTableChange">
                <el-option v-for="t in tableOptions" :key="t.name" :label="t.name" :value="t.name">
                  <span>{{ t.name }}</span><span class="muted" style="margin-left:8px">{{ t.comment || t.tableType }}</span>
                </el-option>
              </el-select>
            </el-form-item>
            <el-form-item label="字典类型列" required>
              <el-select v-model="form.typeCol" class="mono" style="width:260px"
                         filterable allow-create default-first-option
                         :disabled="!form.tableName" :loading="dataColsLoading"
                         placeholder="选择列或手动填写"
                         @visible-change="onDataColsVisible">
                <el-option v-for="c in dataCols" :key="c.name" :label="c.name" :value="c.name">
                  <span>{{ c.name }}</span><span class="muted" style="margin-left:8px">{{ c.comment || c.dataType }}</span>
                </el-option>
              </el-select>
            </el-form-item>
            <el-form-item label="类型名称列">
              <el-select v-model="form.typeLabelCol" class="mono" style="width:260px"
                         filterable allow-create default-first-option
                         :disabled="!form.tableName" :loading="dataColsLoading"
                         placeholder="选择列或手动填写"
                         @visible-change="onDataColsVisible">
                <el-option v-for="c in dataCols" :key="c.name" :label="c.name" :value="c.name">
                  <span>{{ c.name }}</span><span class="muted" style="margin-left:8px">{{ c.comment || c.dataType }}</span>
                </el-option>
              </el-select>
              <span class="muted" style="margin-left:8px">可选，配置后报表里类型下拉显示 名称（编码）</span>
            </el-form-item>
          </template>

          <!-- SQL式 -->
          <template v-if="form.sourceType==='sql'">
            <el-alert type="info" :closable="false" show-icon style="margin-bottom:12px"
                      title="SQL式：自定义查询（最灵活），查询结果中映射出类型/键值/标签三列"></el-alert>
            <el-form-item label="查询 SQL" required>
              <el-input v-model="form.sqlText" type="textarea" :rows="5" class="mono"
                        placeholder="SELECT type_code, dict_value, dict_label FROM ..."></el-input>
            </el-form-item>
            <el-form-item label="类型列" required><el-input v-model="form.typeCol" class="mono" style="width:260px" placeholder="查询结果中字典类型列名"></el-input></el-form-item>
            <el-form-item label="类型名称列">
              <el-input v-model="form.typeLabelCol" class="mono" style="width:260px" placeholder="可选，查询结果中类型名称列名"></el-input>
              <span class="muted" style="margin-left:8px">配置后报表里类型下拉显示 名称（编码）</span>
            </el-form-item>
          </template>

          <!-- 公共：键值列 / 标签列 / 过滤 -->
          <el-form-item :label="form.sourceType==='sql' ? '键值列' : '字典键值列'" required>
            <el-select v-if="form.sourceType!=='sql'" v-model="form.valueCol" class="mono" style="width:260px"
                       filterable allow-create default-first-option
                       :disabled="!colTable" :loading="dataColsLoading"
                       placeholder="选择列或手动填写"
                       @visible-change="onDataColsVisible">
              <el-option v-for="c in dataCols" :key="c.name" :label="c.name" :value="c.name">
                <span>{{ c.name }}</span><span class="muted" style="margin-left:8px">{{ c.comment || c.dataType }}</span>
              </el-option>
            </el-select>
            <el-input v-else v-model="form.valueCol" class="mono" style="width:260px"
                      placeholder="查询结果中键值列名"></el-input>
          </el-form-item>
          <el-form-item :label="form.sourceType==='sql' ? '标签列' : '字典标签列'" required>
            <el-select v-if="form.sourceType!=='sql'" v-model="form.labelCol" class="mono" style="width:260px"
                       filterable allow-create default-first-option
                       :disabled="!colTable" :loading="dataColsLoading"
                       placeholder="选择列或手动填写"
                       @visible-change="onDataColsVisible">
              <el-option v-for="c in dataCols" :key="c.name" :label="c.name" :value="c.name">
                <span>{{ c.name }}</span><span class="muted" style="margin-left:8px">{{ c.comment || c.dataType }}</span>
              </el-option>
            </el-select>
            <el-input v-else v-model="form.labelCol" class="mono" style="width:260px"
                      placeholder="查询结果中标签列名"></el-input>
          </el-form-item>
          <el-form-item v-if="form.sourceType!=='sql'" label="过滤条件">
            <el-input v-model="form.filterWhere" class="mono" style="width:360px" placeholder="可选，如 status = '1'（不含 WHERE 关键字）"></el-input>
          </el-form-item>
          <el-form-item label="状态">
            <el-switch v-model="form.status" :active-value="1" :inactive-value="0" active-text="启用"></el-switch>
          </el-form-item>
          <el-form-item label="备注"><el-input v-model="form.remark" style="width:360px"></el-input></el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="dlgVisible=false">取消</el-button>
          <el-button :loading="testing" @click="test">试跑</el-button>
          <el-button type="primary" :loading="saving" @click="save">保存</el-button>
        </template>
      </el-dialog>

      <!-- 试跑结果 -->
      <el-dialog v-model="testVisible" title="字典试跑（键值 / 标签 / 类型）" width="640px" top="8vh">
        <el-table :data="testRows" border size="small" max-height="50vh" v-loading="testing">
          <el-table-column prop="value" label="字典键值" width="200" class-name="mono"></el-table-column>
          <el-table-column prop="label" label="字典标签"></el-table-column>
          <el-table-column prop="type" label="字典类型" width="180" class-name="mono"></el-table-column>
        </el-table>
        <div class="muted" style="margin-top:8px">共 {{ testRows.length }} 行（最多加载 5000 行）</div>
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
      loading: false, saving: false, testing: false,
      list: [], datasources: [],
      // 表/列元数据缓存（新建/编辑弹窗的下拉选项）
      tableOptions: [], tablesLoading: false,
      typeCols: [], typeColsLoading: false,
      dataCols: [], dataColsLoading: false,
      dlgVisible: false, testVisible: false, testRows: [],
      form: this.emptyForm()
    };
  },
  computed: {
    /**
     * 当前"数据表"：分离式取数据表，聚合式取单表表名；SQL式无表概念返回空。
     * 供键值列/标签列的下拉禁用状态与列加载判断使用。
     */
    colTable: function () {
      var f = this.form;
      return f.sourceType === 'separated' ? f.dataTable : f.tableName;
    }
  },
  created: function () {
    var self = this;
    RS.guard('dict:list').then(function () {
      self.load();
      RS.get('/api/datasources').then(function (d) { self.datasources = d; }).catch(function () {});
    }).catch(function () {});
  },
  methods: {
    emptyForm: function () {
      return { id: 0, code: '', name: '', sourceType: 'aggregate', datasourceId: null,
        typeTable: '', typeCodeCol: '', typeLabelCol: '', dataTable: '', dataFKCol: '',
        tableName: '', typeCol: '', valueCol: '', labelCol: '',
        sqlText: '', filterWhere: '', status: 1, remark: '' };
    },
    load: function () {
      var self = this;
      this.loading = true;
      RS.get('/api/dictionaries').then(function (l) { self.list = l || []; })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.loading = false; });
    },
    openDialog: function (row) {
      if (row) {
        this.form = Object.assign(this.emptyForm(), row);
        this.form.datasourceId = row.datasourceId;
      } else {
        this.form = this.emptyForm();
      }
      // 换对象编辑时重置元数据缓存，列下拉按需重新加载
      this.tableOptions = []; this.typeCols = []; this.dataCols = [];
      this.dlgVisible = true;
    },
    /**
     * 换数据源：清空表/列选择与元数据缓存。
     * 必须清——否则下拉里会留着上一个库的表名/列名，选出来也不属于当前库。
     */
    onDsChange: function () {
      var f = this.form;
      f.typeTable = ''; f.typeCodeCol = ''; f.typeLabelCol = '';
      f.dataTable = ''; f.dataFKCol = '';
      f.tableName = ''; f.typeCol = ''; f.valueCol = ''; f.labelCol = '';
      this.tableOptions = []; this.typeCols = []; this.dataCols = [];
    },
    /**
     * 拉取当前数据源的表清单（含视图），供类型表/数据表/表名下拉使用。
     * 无「数据源查看」权限时接口会被拦，降级为手动填写（allow-create 始终可用）。
     */
    loadTables: function () {
      var self = this;
      if (!this.form.datasourceId) return;
      this.tablesLoading = true;
      RS.get('/api/datasources/' + this.form.datasourceId + '/tables')
        // 后端返回 {items,total,truncated,cached,ageSec}（表清单有 TTL 缓存）
        .then(function (res) { self.tableOptions = (res && res.items) || []; })
        .catch(function (e) {
          self.tableOptions = [];
          if (String((e && e.message) || '').indexOf('权限') >= 0) {
            ElMessage.warning('当前账号没有浏览表结构的权限，可手动填写表名/列名');
          } else { RS.alertErr(e); }
        })
        .finally(function () { self.tablesLoading = false; });
    },
    /** 表下拉展开时按需加载表清单 */
    onTablesVisible: function (v) {
      if (v && this.form.datasourceId && !this.tableOptions.length) this.loadTables();
    },
    /**
     * 拉取指定表的列清单。
     * @param {string} which 'type'=类型表列（分离式类型编码/名称列）；'data'=数据表列（分离式取数据表，聚合式取单表）
     */
    loadCols: function (which) {
      var self = this;
      var table = which === 'type' ? this.form.typeTable : this.colTable;
      if (!table || !this.form.datasourceId) return;
      if (which === 'type') this.typeColsLoading = true; else this.dataColsLoading = true;
      RS.get('/api/datasources/' + this.form.datasourceId + '/tables/' + encodeURIComponent(table) + '/columns')
        .then(function (res) {
          var l = (res && res.items) || [];
          if (which === 'type') { self.typeCols = l; } else { self.dataCols = l; }
        })
        .catch(function (e) {
          if (which === 'type') { self.typeCols = []; } else { self.dataCols = []; }
          if (String((e && e.message) || '').indexOf('权限') < 0) RS.alertErr(e);
        })
        .finally(function () {
          if (which === 'type') { self.typeColsLoading = false; } else { self.dataColsLoading = false; }
        });
    },
    /** 类型表列下拉展开时按需加载 */
    onTypeColsVisible: function (v) {
      if (v && this.form.typeTable && !this.typeCols.length) this.loadCols('type');
    },
    /** 数据表列下拉展开时按需加载 */
    onDataColsVisible: function (v) {
      if (v && this.colTable && !this.dataCols.length) this.loadCols('data');
    },
    /** 换类型表：清依赖的类型列字段并预加载列清单 */
    onTypeTableChange: function () {
      this.form.typeCodeCol = ''; this.form.typeLabelCol = '';
      this.typeCols = [];
      if (this.form.typeTable) this.loadCols('type');
    },
    /** 换数据表：清依赖的数据表列字段并预加载列清单 */
    onDataTableChange: function () {
      this.form.dataFKCol = ''; this.form.valueCol = ''; this.form.labelCol = '';
      this.dataCols = [];
      if (this.form.dataTable) this.loadCols('data');
    },
    /** 聚合式换单表：清依赖的列字段并预加载列清单 */
    onAggTableChange: function () {
      this.form.typeCol = ''; this.form.typeLabelCol = '';
      this.form.valueCol = ''; this.form.labelCol = '';
      this.dataCols = [];
      if (this.form.tableName) this.loadCols('data');
    },
    validate: function () {
      var f = this.form;
      if (!f.code) return '请填写字典编码';
      if (!/^[a-zA-Z][a-zA-Z0-9_]*$/.test(f.code)) return '字典编码仅允许字母、数字、下划线';
      if (!f.name) return '请填写字典名称';
      if (!f.datasourceId) return '请选择数据源';
      if (f.sourceType === 'separated') {
        if (!f.typeTable || !f.typeCodeCol || !f.dataTable || !f.dataFKCol) return '分离式：类型表/类型编码列/数据表/关联字段必填';
      }
      if (f.sourceType === 'aggregate' && (!f.tableName || !f.typeCol)) return '聚合式：表名与字典类型列必填';
      if (f.sourceType === 'sql' && !f.sqlText.trim()) return 'SQL式：请填写查询语句';
      if (!f.valueCol || !f.labelCol) return '键值列与标签列必填';
      return null;
    },
    save: function () {
      var self = this;
      var err = this.validate();
      if (err) { ElMessage.warning(err); return; }
      this.saving = true;
      var p = this.form.id ? RS.put('/api/dictionaries/' + this.form.id, this.form)
                           : RS.post('/api/dictionaries', this.form);
      p.then(function () {
        ElMessage.success('已保存');
        self.dlgVisible = false;
        self.load();
      }).catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.saving = false; });
    },
    /**
     * 列表行试跑：用数据库里已保存的字典配置直接取数（与弹窗内 test 的区别是
     * 不校验、不依赖表单，可验证"保存的配置"是否正确）。
     */
    preview: function (row) {
      var self = this;
      this.testing = true;
      this.testVisible = true;
      this.testRows = [];
      RS.post('/api/dictionaries/test', Object.assign(this.emptyForm(), row))
        .then(function (rows) { self.testRows = rows || []; })
        .catch(function (e) { self.testVisible = false; RS.alertErr(e); })
        .finally(function () { self.testing = false; });
    },
    test: function () {
      var self = this;
      var err = this.validate();
      if (err) { ElMessage.warning(err); return; }
      this.testing = true;
      this.testVisible = true;
      RS.post('/api/dictionaries/test', this.form)
        .then(function (rows) { self.testRows = rows || []; })
        .catch(function (e) { self.testVisible = false; RS.alertErr(e); })
        .finally(function () { self.testing = false; });
    },
    remove: function (row) {
      var self = this;
      ElMessageBox.confirm('删除字典「' + row.name + '」？引用它的报表将失去选项/翻译', '确认删除')
        .then(function () { return RS.del('/api/dictionaries/' + row.id); })
        .then(function () { ElMessage.success('已删除'); self.load(); })
        .catch(function (e) { if (e !== 'cancel' && e !== 'close') RS.alertErr(e); });
    }
  }
}
</script>
