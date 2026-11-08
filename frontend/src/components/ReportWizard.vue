<!--
  ReportWizard.vue - 从数据库表快速生成报表草稿（三步向导）
  步骤：选数据源/表 → 勾字段（别名/显示名/排序）→ 预览 SQL 并创建草稿
  说明：SQL 生成由后端 /api/report-wizard/generate 统一完成（引号处理、跨模式表名、
        默认按主键排序、条件草稿都按报表引擎的既有规则来），本组件只负责交互与组装保存请求。
-->
<template>
  <el-dialog v-model="visible" title="从表建报表" width="920px" top="6vh" :close-on-click-modal="false"
             @closed="onClosed">
    <el-steps :active="step" simple style="margin-bottom:16px">
      <el-step title="选择数据表"></el-step>
      <el-step title="选择字段"></el-step>
      <el-step title="预览并创建"></el-step>
    </el-steps>

    <!-- 第 1 步：数据源 + 表 -->
    <div v-if="step === 0">
      <el-form label-width="90px">
        <el-form-item label="数据源" required>
          <el-select v-model="dsId" placeholder="请选择数据源" style="width:320px" @change="onDsChange">
            <el-option v-for="d in datasources" :key="d.id" :label="d.name + '（' + d.dbType + '）'" :value="d.id"></el-option>
          </el-select>
        </el-form-item>
        <el-form-item label="数据表" required>
          <el-select v-model="table" filterable remote clearable :remote-method="searchTables"
                     :loading="tablesLoading" style="width:420px" placeholder="输入表名关键字搜索"
                     :disabled="!dsId" @change="onTableChange"
                     @visible-change="(v) => { if (v && !tables.length) loadTables('') }">
            <el-option v-for="t in tables" :key="t.name" :label="t.name" :value="t.name">
              <span>{{ t.name }}</span>
              <span class="muted" style="margin-left:8px">{{ (t.comment || '').trim() || t.tableType }}</span>
            </el-option>
          </el-select>
          <div class="muted" style="margin-left:10px">
            {{ tableTotal }} 张表{{ tableTruncated ? '（已截断，请用关键字精确搜索）' : '' }}
          </div>
        </el-form-item>
        <el-form-item v-if="table">
          <div class="muted">
            <template v-if="tableComment"><b>{{ tableComment }}</b> · </template>
            <span class="mono">{{ table }}</span> · 已加载 {{ columns.length }} 个字段
            <template v-if="pkNames.length">，主键：<span class="mono">{{ pkNames.join(', ') }}</span></template>
            <template v-else-if="columns.length">，<b>未检测到主键</b>（分页时请指定排序字段，否则可能翻页重复或漏行）</template>
          </div>
        </el-form-item>
      </el-form>
    </div>

    <!-- 第 2 步：字段 -->
    <div v-else-if="step === 1">
      <div class="toolbar" style="flex-wrap:wrap;gap:8px;margin-bottom:8px">
        <el-input v-model="colKeyword" placeholder="筛选字段" clearable style="width:180px"></el-input>
        <el-checkbox :model-value="allChecked" :indeterminate="someChecked" @change="toggleAll">
          全选（{{ columns.length }} 个）
        </el-checkbox>
        <span class="muted">已选 {{ checkedCount }} / {{ columns.length }}</span>
        <el-switch v-model="distinct" active-text="去重（DISTINCT）" style="margin-left:auto"></el-switch>
      </div>

      <el-table :data="pagedColumns" height="340" border size="small" v-loading="colsLoading">
        <el-table-column width="46">
          <template #default="{ row }">
            <el-checkbox v-model="row._checked"></el-checkbox>
          </template>
        </el-table-column>
        <el-table-column prop="name" label="字段" min-width="180">
          <template #default="{ row }">
            <span class="mono">{{ row.name }}</span>
            <el-tag v-if="row.isPk" size="small" type="danger" style="margin-left:6px">PK</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="dataType" label="类型" width="130" show-overflow-tooltip></el-table-column>
        <el-table-column prop="comment" label="注释" min-width="140" show-overflow-tooltip>
          <template #default="{ row }">{{ (row.comment || '').trim() || '—' }}</template>
        </el-table-column>
        <el-table-column label="别名" width="140">
          <template #default="{ row }">
            <el-input v-model="row._alias" size="small" placeholder="可空" :disabled="!row._checked"></el-input>
          </template>
        </el-table-column>
        <el-table-column label="显示名" width="150">
          <template #default="{ row }">
            <el-input v-model="row._label" size="small" placeholder="默认取注释" :disabled="!row._checked"></el-input>
          </template>
        </el-table-column>
      </el-table>

      <!-- 宽表（几百上千列）分页显示：el-table 无虚拟滚动，一次渲染上千行会明显卡顿 -->
      <div v-if="filteredColumns.length > colPageSize" class="toolbar" style="margin-top:8px">
        <el-pagination size="small" layout="total, prev, pager, next"
                       :total="filteredColumns.length" :page-size="colPageSize"
                       v-model:current-page="colPage"></el-pagination>
      </div>

      <div class="toolbar" style="margin-top:12px;gap:8px">
        <span class="muted">排序字段：</span>
        <el-select v-model="orderFields" multiple filterable placeholder="不选则按主键排序" style="width:320px">
          <el-option v-for="c in checkedColumns" :key="c.name"
                     :label="c._alias || c.name" :value="c._alias || c.name"></el-option>
        </el-select>
        <el-radio-group v-model="orderDir" :disabled="!orderFields.length">
          <el-radio-button value="asc">升序</el-radio-button>
          <el-radio-button value="desc">降序</el-radio-button>
        </el-radio-group>
      </div>
    </div>

    <!-- 第 3 步：预览并创建 -->
    <div v-else>
      <el-form label-width="90px" style="margin-bottom:8px">
        <el-form-item label="报表名称" required>
          <el-input v-model="name" placeholder="默认取「表注释（无则表名）+ 列表」" style="width:340px"></el-input>
        </el-form-item>
        <el-form-item label="分组" required>
          <el-select v-model="groupId" placeholder="请选择分组" style="width:240px">
            <el-option v-for="g in groups" :key="g.id" :label="g.name" :value="g.id"></el-option>
          </el-select>
          <span class="muted" style="margin-left:10px">创建后为「草稿」，可在配置页继续调整展示列与条件</span>
        </el-form-item>
      </el-form>

      <div v-if="!result" class="muted" style="padding:20px 0">
        <el-button type="primary" :loading="generating" @click="generate">生成预览</el-button>
      </div>

      <template v-else>
        <el-alert v-for="(w, i) in result.warnings" :key="i" :title="w" type="warning"
                  :closable="false" show-icon style="margin-bottom:6px"></el-alert>

        <div class="muted" style="margin:8px 0 4px">生成的 SQL（只读，创建后可在配置页修改）</div>
        <pre class="cfg-sql-preview mono" style="max-height:200px">{{ result.sqlText }}</pre>

        <div class="muted" style="margin:10px 0 4px">
          展示列 {{ (result.columns || []).length }} 个，查询条件草稿 {{ (result.params || []).length }} 个
        </div>
        <el-table v-if="(result.params || []).length" :data="result.params" size="small" border max-height="180">
          <el-table-column prop="paramName" label="参数名" width="110"></el-table-column>
          <el-table-column prop="columnName" label="列名" min-width="140"></el-table-column>
          <el-table-column prop="label" label="显示名" min-width="140"></el-table-column>
          <el-table-column prop="widget" label="控件" width="90"></el-table-column>
          <el-table-column prop="matchType" label="匹配" width="80"></el-table-column>
        </el-table>
      </template>
    </div>

    <template #footer>
      <el-button @click="close">取消</el-button>
      <el-button v-if="step > 0" @click="step--">上一步</el-button>
      <el-button v-if="step === 0" type="primary" :disabled="!table" @click="step = 1">下一步</el-button>
      <el-button v-else-if="step === 1" type="primary" :disabled="!checkedCount" @click="toPreview">下一步</el-button>
      <el-button v-else-if="!result" type="primary" :loading="generating" @click="generate">生成预览</el-button>
      <el-button v-else type="primary" :loading="creating" @click="create">创建草稿报表</el-button>
    </template>
  </el-dialog>
</template>

<script>
import { ElMessage } from 'element-plus'
import { RS } from '@/lib/rs.js'

export default {
  data: function () {
    return {
      visible: false,
      step: 0,
      datasources: [], dsId: null,
      tables: [], tablesLoading: false, tableTotal: 0, tableTruncated: false, timer: 0,
      table: '', columns: [], colsLoading: false,
      colKeyword: '', distinct: false,
      // 宽表分页：几百上千列的实绩表一次渲染全部行会让弹窗卡住数秒。
      // 每页 100 行——每行含 2 个 el-input（别名/显示名），行数再多组件实例开销明显。
      colPage: 1, colPageSize: 100,
      orderFields: [], orderDir: 'asc',
      groups: [], roleCodes: [],
      name: '', groupId: null,
      result: null, generating: false, creating: false
    }
  },
  computed: {
    checkedColumns: function () { return this.columns.filter(function (c) { return c._checked }) },
    checkedCount: function () { return this.checkedColumns.length },
    /** 主键字段名（第 1 步提示用：有主键才会自动生成 ORDER BY） */
    pkNames: function () {
      return this.columns.filter(function (c) { return c.isPk }).map(function (c) { return c.name })
    },
    /** 所选表的注释（第 1 步提示用；没有注释返回空串，模板据此隐藏） */
    tableComment: function () {
      var name = this.table
      if (!name) return ''
      var t = this.tables.filter(function (x) { return x.name === name })[0]
      return t && t.comment ? String(t.comment).trim() : ''
    },
    allChecked: function () { return this.columns.length > 0 && this.checkedCount === this.columns.length },
    someChecked: function () { return this.checkedCount > 0 && this.checkedCount < this.columns.length },
    filteredColumns: function () {
      var kw = (this.colKeyword || '').trim().toLowerCase()
      if (!kw) return this.columns
      return this.columns.filter(function (c) {
        return (c.name || '').toLowerCase().indexOf(kw) >= 0 ||
          ((c.comment || '') + '').toLowerCase().indexOf(kw) >= 0
      })
    },
    /** 当前页的字段（模板绑定）。页码超出范围时自动收敛，避免筛选后停在空白页 */
    pagedColumns: function () {
      var list = this.filteredColumns
      var size = this.colPageSize
      var maxPage = Math.max(1, Math.ceil(list.length / size))
      var p = Math.min(Math.max(1, this.colPage), maxPage)
      return list.slice((p - 1) * size, p * size)
    }
  },
  watch: {
    // 换关键字后结果集会变，页码必须回第 1 页，否则可能停在空白页
    colKeyword: function () { this.colPage = 1 }
  },
  methods: {
    /** 打开向导（父组件通过 ref 调用） */
    open: function () {
      this.step = 0
      this.result = null
      this.dsId = null
      this.table = ''
      this.tables = []
      this.columns = []
      this.orderFields = []
      this.colKeyword = ''
      this.colPage = 1
      this.distinct = false
      this.name = ''
      this.visible = true
      this.loadRefs()
    },
    close: function () { this.visible = false },
    onClosed: function () { this.result = null },
    loadRefs: function () {
      var self = this
      RS.get('/api/datasources').then(function (d) { self.datasources = d || [] }).catch(function (e) { RS.alertErr(e) })
      RS.get('/api/report-groups').then(function (g) {
        self.groups = g || []
        if (!self.groupId && self.groups.length) self.groupId = self.groups[0].id
      }).catch(function () {})
      // 新建报表默认授予全部启用角色（与配置页保持一致，避免创建后无人可见）
      RS.get('/api/roles').then(function (roles) {
        self.roleCodes = (roles || []).map(function (r) { return r.roleKey })
      }).catch(function () {})
    },
    onDsChange: function () {
      this.table = ''
      this.tables = []
      this.columns = []
      if (this.dsId) this.loadTables('')
    },
    /** 表清单：后端有 TTL 缓存，关键字在服务端内存过滤，配合 250ms 防抖即可 */
    loadTables: function (kw) {
      var self = this
      if (!this.dsId) return
      this.tablesLoading = true
      var qs = []
      if (kw) qs.push('keyword=' + encodeURIComponent(kw))
      RS.get('/api/datasources/' + this.dsId + '/tables' + (qs.length ? '?' + qs.join('&') : ''))
        .then(function (res) {
          self.tables = (res && res.items) || []
          self.tableTotal = (res && res.total) || self.tables.length
          self.tableTruncated = !!(res && res.truncated)
        })
        .catch(function (e) { self.tables = []; RS.alertErr(e) })
        .finally(function () { self.tablesLoading = false })
    },
    searchTables: function (kw) {
      var self = this
      clearTimeout(this.timer)
      this.timer = setTimeout(function () { self.loadTables(kw || '') }, 250)
    },
    onTableChange: function (name) {
      var self = this
      this.columns = []
      this.orderFields = []
      this.colPage = 1
      if (!name) return
      // 报表名默认「表注释 + 列表」，没有注释才退回表名——注释通常比物理表名可读
      if (!this.name) {
        var t = this.tables.filter(function (x) { return x.name === name })[0]
        var cmt = t && t.comment ? String(t.comment).trim() : ''
        this.name = (cmt || name) + ' 列表'
      }
      this.colsLoading = true
      RS.get('/api/datasources/' + this.dsId + '/tables/' + encodeURIComponent(name) + '/columns')
        .then(function (res) {
          var items = (res && res.items) || []
          // 默认全选 + 显示名取注释：大多数报表要的就是全字段
          self.columns = items.map(function (c) {
            return {
              name: c.name, dataType: c.dataType, comment: (c.comment || '').trim(),
              isPk: !!c.isPk, _checked: true, _alias: '', _label: (c.comment || '').trim()
            }
          })
        })
        .catch(function (e) { RS.alertErr(e) })
        .finally(function () { self.colsLoading = false })
    },
    toggleAll: function (v) {
      this.columns.forEach(function (c) { c._checked = !!v })
    },
    toPreview: function () {
      // 排序字段只保留仍然勾选的（取消勾选后不该继续出现在排序里）
      var names = this.checkedColumns.map(function (c) { return c._alias || c.name })
      this.orderFields = this.orderFields.filter(function (n) { return names.indexOf(n) >= 0 })
      this.step = 2
      if (!this.result) this.generate()
    },
    generate: function () {
      var self = this
      if (!this.dsId || !this.table) { ElMessage.warning('请先选择数据表和字段'); return }
      var cols = this.checkedColumns.map(function (c) {
        return { name: c.name, alias: c._alias || '', label: c._label || '' }
      })
      if (!cols.length) { ElMessage.warning('请至少选择一个字段'); return }
      this.generating = true
      RS.post('/api/report-wizard/generate', {
        datasourceId: this.dsId,
        table: this.table,
        columns: cols,
        distinct: this.distinct,
        orderBy: this.orderFields.map(function (n) { return { name: n, desc: self.orderDir === 'desc' } }),
        withParams: true
      })
        .then(function (res) { self.result = res })
        .catch(function (e) { self.result = null; RS.alertErr(e) })
        .finally(function () { self.generating = false })
    },
    create: function () {
      var self = this
      if (!this.name) { ElMessage.warning('请填写报表名称'); return }
      if (!this.groupId) { ElMessage.warning('请选择分组'); return }
      this.creating = true
      RS.post('/api/reports', {
        name: this.name,
        groupId: this.groupId,
        datasourceId: this.dsId,
        sqlText: this.result.sqlText,
        params: this.result.params || [],
        columns: this.result.columns || [],
        roleCodes: this.roleCodes
      })
        .then(function (res) {
          ElMessage.success('已创建草稿报表')
          self.visible = false
          self.$emit('created', res && res.id)
        })
        .catch(function (e) { RS.alertErr(e) })
        .finally(function () { self.creating = false })
    }
  }
}
</script>
