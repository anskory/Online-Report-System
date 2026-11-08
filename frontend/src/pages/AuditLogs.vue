<!--
  AuditLogs.vue - 操作日志（审计日志查询）
  - 服务端分页 + 多条件筛选（类型/模块/用户/关键字/结果/日期范围）
  - 详情抽屉展示完整 Detail / 失败原因 / UA / TraceID
  - 导出 CSV 与按日期清理，分别受 system:audit:export / system:audit:delete 控制
-->
<template>
  <div>
    <div class="page-body">
      <!-- 筛选条 -->
      <div class="toolbar" style="flex-wrap:wrap;gap:8px">
        <el-select v-model="filters.logType" placeholder="全部类型" clearable style="width:130px">
          <el-option v-for="t in typeOptions" :key="t.v" :label="t.t" :value="t.v"></el-option>
        </el-select>
        <el-select v-model="filters.module" placeholder="全部模块" clearable style="width:130px">
          <el-option v-for="m in moduleOptions" :key="m.v" :label="m.t" :value="m.v"></el-option>
        </el-select>
        <el-input v-model="filters.username" placeholder="用户名" clearable style="width:140px"
                  @keyup.enter="search" @clear="search"></el-input>
        <el-input v-model="filters.keyword" placeholder="详情/目标关键字" clearable style="width:180px"
                  @keyup.enter="search" @clear="search"></el-input>
        <el-select v-model="filters.success" placeholder="全部结果" clearable style="width:120px">
          <el-option label="成功" value="1"></el-option>
          <el-option label="失败" value="0"></el-option>
        </el-select>
        <el-date-picker v-model="filters.range" type="daterange" value-format="YYYY-MM-DD"
                        start-placeholder="开始日期" end-placeholder="结束日期"
                        style="width:240px" unlink-panels></el-date-picker>
        <el-button type="primary" @click="search">查询</el-button>
        <el-button @click="reset">重置</el-button>
        <el-button v-if="can('system:audit:export')" :loading="exporting" @click="exportCsv">导出 CSV</el-button>
        <el-button v-if="can('system:audit:delete')" type="danger" plain @click="openClean">清理</el-button>
      </div>

      <!-- 概览：最近 7 天的量级，帮助判断日志是否异常膨胀 -->
      <div class="muted" style="margin-bottom:10px">
        近 {{ stats.days || 7 }} 天共 <b>{{ stats.total || 0 }}</b> 条，失败 <b>{{ stats.failed || 0 }}</b> 条
        <span v-if="typeStatsText">（{{ typeStatsText }}）</span>
      </div>

      <el-table :data="list" v-loading="loading" border stripe size="small">
        <el-table-column label="时间" width="165">
          <template #default="s"><span class="mono">{{ fmtTime(s.row.createdAt) }}</span></template>
        </el-table-column>
        <el-table-column label="类型" width="100">
          <template #default="s">
            <el-tag :type="typeTag(s.row.logType)" size="small">{{ typeLabel(s.row.logType) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="module" label="模块" width="100">
          <template #default="s">{{ moduleLabel(s.row.module) }}</template>
        </el-table-column>
        <el-table-column prop="username" label="用户" width="130">
          <template #default="s">{{ s.row.username || '—' }}</template>
        </el-table-column>
        <el-table-column prop="clientIP" label="IP" width="130"></el-table-column>
        <el-table-column prop="target" label="目标" min-width="160" show-overflow-tooltip>
          <template #default="s">{{ s.row.target || '—' }}</template>
        </el-table-column>
        <el-table-column label="耗时" width="90" align="right">
          <template #default="s">
            <span v-if="s.row.costMs">{{ s.row.costMs }} ms</span>
            <span v-else class="muted">—</span>
          </template>
        </el-table-column>
        <el-table-column label="结果" width="80">
          <template #default="s">
            <el-tag v-if="s.row.success === 1" type="success" size="small">成功</el-tag>
            <el-tag v-else type="danger" size="small">失败</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="详情" min-width="240" show-overflow-tooltip>
          <template #default="s"><span class="mono">{{ s.row.detail }}</span></template>
        </el-table-column>
        <el-table-column label="操作" width="80" fixed="right">
          <template #default="s">
            <el-button size="small" link type="primary" @click="openDetail(s.row)">详情</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-pagination style="margin-top:12px;justify-content:flex-end"
                     background layout="total, sizes, prev, pager, next"
                     :total="total" :current-page="page" :page-size="pageSize"
                     :page-sizes="[20, 50, 100, 200]"
                     @current-change="onPage" @size-change="onSize"></el-pagination>
    </div>

    <!-- 详情 -->
    <el-drawer v-model="detailVisible" title="日志详情" size="520px">
      <el-descriptions v-if="detail.id" :column="1" border size="small">
        <el-descriptions-item label="时间">{{ fmtTime(detail.createdAt) }}</el-descriptions-item>
        <el-descriptions-item label="类型">{{ typeLabel(detail.logType) }}</el-descriptions-item>
        <el-descriptions-item label="模块">{{ moduleLabel(detail.module) || '—' }}</el-descriptions-item>
        <el-descriptions-item label="用户">{{ detail.username || '匿名' }}（ID {{ detail.userId }}）</el-descriptions-item>
        <el-descriptions-item label="IP">{{ detail.clientIP || '—' }}</el-descriptions-item>
        <el-descriptions-item label="目标">{{ detail.target || '—' }}</el-descriptions-item>
        <el-descriptions-item label="报表 ID">{{ detail.reportId || '—' }}</el-descriptions-item>
        <el-descriptions-item label="数据源 ID">{{ detail.datasourceId || '—' }}</el-descriptions-item>
        <el-descriptions-item label="耗时">{{ detail.costMs ? detail.costMs + ' ms' : '—' }}</el-descriptions-item>
        <el-descriptions-item label="结果">
          <el-tag v-if="detail.success === 1" type="success" size="small">成功</el-tag>
          <el-tag v-else type="danger" size="small">失败</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="TraceID">
          <span class="mono">{{ detail.traceId || '—' }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="UA">
          <span class="mono" style="word-break:break-all">{{ detail.userAgent || '—' }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="详情">
          <pre class="mono" style="white-space:pre-wrap;word-break:break-all;margin:0">{{ detail.detail }}</pre>
        </el-descriptions-item>
        <el-descriptions-item v-if="detail.errMsg" label="失败原因">
          <span style="color:var(--el-color-danger)">{{ detail.errMsg }}</span>
        </el-descriptions-item>
      </el-descriptions>
    </el-drawer>

    <!-- 清理 -->
    <el-dialog v-model="cleanVisible" title="清理历史日志" width="420px">
      <div class="muted" style="margin-bottom:10px">
        将删除所选日期<b>之前</b>的全部日志，删除后不可恢复。
      </div>
      <el-date-picker v-model="cleanBefore" type="date" value-format="YYYY-MM-DD"
                      placeholder="选择截止日期" style="width:100%"></el-date-picker>
      <template #footer>
        <el-button @click="cleanVisible = false">取消</el-button>
        <el-button type="danger" :loading="cleaning" @click="doClean">确定清理</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script>
import { ElMessage, ElMessageBox } from 'element-plus'
import { RS } from '@/lib/rs.js'

/** 日志类型 → 中文名 */
const TYPE_LABEL = { login: '登录', query: '查询', export: '导出', config_change: '配置变更' }
/** 日志类型 → 标签配色 */
const TYPE_TAG = { login: 'info', query: 'primary', export: 'warning', config_change: 'danger' }
/** 模块 → 中文名（与后端埋点的 Module 取值保持一致） */
const MODULE_LABEL = {
  report: '报表', datasource: '数据源', dict: '字典',
  user: '用户', role: '角色', menu: '菜单',
  system: '系统', export: '导出'
}

export default {
  data: function () {
    return {
      list: [], total: 0, page: 1, pageSize: 20, loading: false,
      filters: { logType: '', module: '', username: '', keyword: '', success: '', range: [] },
      stats: {},
      detailVisible: false, detail: {},
      exporting: false,
      cleanVisible: false, cleaning: false, cleanBefore: '',
      typeOptions: [
        { v: 'login', t: '登录' }, { v: 'query', t: '查询' },
        { v: 'export', t: '导出' }, { v: 'config_change', t: '配置变更' }
      ],
      moduleOptions: [
        { v: 'report', t: '报表' }, { v: 'datasource', t: '数据源' },
        { v: 'dict', t: '字典' }, { v: 'user', t: '用户' },
        { v: 'role', t: '角色' }, { v: 'menu', t: '菜单' },
        { v: 'system', t: '系统' }, { v: 'export', t: '导出' }
      ]
    }
  },
  computed: {
    /** 概览里的「类型分布」摘要，避免为它单开一个图表 */
    typeStatsText: function () {
      var arr = this.stats.byType || []
      return arr.map(function (x) { return TYPE_LABEL[x.key] || x.key; })
        .filter(Boolean).join(' / ')
    }
  },
  mounted: function () {
    // 清理默认截止日期：90 天前（与服务端默认保留期一致）
    this.cleanBefore = this.shiftDate(-90)
    this.load()
    this.loadStats()
  },
  methods: {
    // 模板里的 can('...') 直接走 RS 权限点判断（不要依赖 $root 上的全局属性）
    can: function (perm) { return RS.can(perm) },
    typeLabel: function (v) { return TYPE_LABEL[v] || v || '—' },
    typeTag: function (v) { return TYPE_TAG[v] || 'info' },
    moduleLabel: function (v) { return MODULE_LABEL[v] || v || '—' },
    fmtTime: function (v) {
      if (!v) return '—'
      var d = new Date(v)
      if (isNaN(d.getTime())) return String(v)
      var p = function (n) { return n < 10 ? '0' + n : '' + n }
      return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) + ' ' +
        p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds())
    },
    /** 生成相对今天的日期串（YYYY-MM-DD），用于清理默认值 */
    shiftDate: function (days) {
      var d = new Date()
      d.setDate(d.getDate() + days)
      var p = function (n) { return n < 10 ? '0' + n : '' + n }
      return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate())
    },
    /** 组装查询串（空值不传） */
    queryString: function () {
      var f = this.filters
      var p = []
      var add = function (k, v) { if (v !== '' && v !== null && v !== undefined) p.push(k + '=' + encodeURIComponent(v)) }
      add('logType', f.logType)
      add('module', f.module)
      add('username', f.username)
      add('keyword', f.keyword)
      add('success', f.success)
      if (f.range && f.range.length === 2) { add('from', f.range[0]); add('to', f.range[1]) }
      return p.join('&')
    },
    load: function () {
      var self = this
      this.loading = true
      var qs = this.queryString()
      var url = '/api/system/audit-logs?page=' + this.page + '&pageSize=' + this.pageSize + (qs ? '&' + qs : '')
      RS.get(url)
        .then(function (res) {
          self.list = (res && res.items) || []
          self.total = (res && res.total) || 0
        })
        .catch(function (e) { RS.alertErr(e) })
        .finally(function () { self.loading = false })
    },
    loadStats: function () {
      var self = this
      RS.get('/api/system/audit-logs/stats?days=7')
        .then(function (res) { self.stats = res || {} })
        .catch(function () { /* 概览失败不影响主流程 */ })
    },
    search: function () { this.page = 1; this.load() },
    reset: function () {
      this.filters = { logType: '', module: '', username: '', keyword: '', success: '', range: [] }
      this.page = 1
      this.load()
    },
    onPage: function (p) { this.page = p; this.load() },
    onSize: function (s) { this.pageSize = s; this.page = 1; this.load() },
    openDetail: function (row) { this.detail = row; this.detailVisible = true },

    /**
     * 导出 CSV：接口要带 Authorization 头，普通 <a download> 拿不到令牌，
     * 所以用 fetch 取 blob 再触发下载。
     */
    exportCsv: function () {
      var self = this
      var qs = this.queryString()
      var url = '/api/system/audit-logs/export' + (qs ? '?' + qs : '')
      this.exporting = true
      fetch(url, { headers: { Authorization: 'Bearer ' + RS.token() } })
        .then(function (res) {
          if (!res.ok) throw new Error('导出失败（HTTP ' + res.status + '）')
          return res.blob()
        })
        .then(function (blob) {
          var a = document.createElement('a')
          a.href = URL.createObjectURL(blob)
          a.download = 'audit-logs-' + self.shiftDate(0) + '.csv'
          document.body.appendChild(a)
          a.click()
          document.body.removeChild(a)
          URL.revokeObjectURL(a.href)
          ElMessage.success('已开始下载')
        })
        .catch(function (e) { RS.alertErr(e) })
        .finally(function () { self.exporting = false })
    },

    openClean: function () {
      // 弹窗里点「确定清理」时再做一次确认，避免两次确认框
      this.cleanVisible = true
    },
    doClean: function () {
      var self = this
      if (!this.cleanBefore) { ElMessage.warning('请选择截止日期'); return }
      // 不可恢复操作：删除前再确认一次
      ElMessageBox.confirm('将删除 ' + this.cleanBefore + ' 之前的全部日志，不可恢复，确定继续？',
        '清理确认', { type: 'warning', confirmButtonText: '确定清理', cancelButtonText: '取消' })
        .then(function () {
          self.cleaning = true
          return RS.del('/api/system/audit-logs?before=' + encodeURIComponent(self.cleanBefore))
        })
        .then(function (res) {
          ElMessage.success('已清理 ' + ((res && res.deleted) || 0) + ' 条日志')
          self.cleanVisible = false
          self.page = 1
          self.load()
          self.loadStats()
        })
        .catch(function (e) {
          if (e !== 'cancel' && e !== 'close') RS.alertErr(e)
        })
        .finally(function () { self.cleaning = false })
    }
  }
}
</script>
