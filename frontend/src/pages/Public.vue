<!--
  Public.vue - 报表查看
  模板与逻辑均由迁移前的 public.html（in-DOM 模板）与内联脚本原样搬入，业务逻辑未改动。
-->
<template>
  <!-- rs-page-fill：整页固定高度，剩余空间给表格，数据在表格内部滚动 -->
  <div class="rs-page-fill">
    <div class="page-header">
      <span class="brand">{{ info.name || '报表查看' }}</span>
      <span class="right muted">公开访问</span>
    </div>
    <!-- 遮罩不挂整页：抬头、查询条件与表格区各自独立，加载时只有表格区被遮挡 -->
    <div class="page-body">
      <template v-if="denied">
        <el-result icon="warning" title="链接无效" sub-title="公开链接或令牌已失效，请联系报表管理员"></el-result>
      </template>
      <template v-else>
        <el-alert v-if="info.remark" :title="info.remark" type="info" :closable="false" style="margin-bottom:12px"></el-alert>

        <!-- 查询条件（后端 /info 已返回脱敏后的条件配置，用于匿名筛选；可通过右上角按钮隐藏） -->
        <el-card class="query-card" shadow="never" v-if="params.length" v-show="searchVisible">
          <el-form inline size="small" @submit.prevent="doQuery(1)">
            <el-form-item v-for="p in params" :key="p.id" :label="p.label">
              <!-- 文本/数字：支持多行输入多值搜索（换行/逗号分隔，查询时拆成数组按 IN 展开） -->
              <el-input v-if="p.widget==='text'" v-model="form[p.paramName]" type="textarea"
                        :autosize="{ minRows: 1, maxRows: 6 }"
                        :placeholder="p.label + '（可多行，每行一个值）'"
                        style="width:200px"></el-input>
              <el-input v-else-if="p.widget==='number'" v-model="form[p.paramName]" type="textarea"
                        :autosize="{ minRows: 1, maxRows: 6 }"
                        :placeholder="p.label + '（可多行，每行一个数值）'"
                        style="width:170px"></el-input>
              <el-select v-else-if="p.widget==='select'" v-model="form[p.paramName]"
                         style="width:180px" clearable filterable
                         :loading="optionLoading[p.paramName]"
                         @visible-change="(v) => { if(v) loadOptions(p) }">
                <el-option v-for="o in options[p.paramName] || fixedOptions(p)" :key="String(o.value)"
                           :label="o.label" :value="o.value"></el-option>
              </el-select>
              <el-select v-else-if="p.widget==='multiSelect'" v-model="form[p.paramName]" multiple
                         style="width:240px" collapse-tags collapse-tags-tooltip filterable clearable
                         :loading="optionLoading[p.paramName]"
                         @visible-change="(v) => { if(v) loadOptions(p) }">
                <el-option v-for="o in options[p.paramName] || fixedOptions(p)" :key="String(o.value)"
                           :label="o.label" :value="o.value"></el-option>
              </el-select>
              <el-date-picker v-else-if="p.widget==='date'" v-model="form[p.paramName]" type="date"
                              value-format="YYYY-MM-DD" style="width:160px"></el-date-picker>
              <el-date-picker v-else-if="p.widget==='dateTime'" v-model="form[p.paramName]" type="datetime"
                              value-format="YYYY-MM-DD HH:mm:ss" style="width:200px"></el-date-picker>
              <el-date-picker v-else-if="p.widget==='dateRange'" v-model="form[p.paramName]" type="daterange"
                              value-format="YYYY-MM-DD" range-separator="至"
                              start-placeholder="开始" end-placeholder="结束" style="width:260px"></el-date-picker>
              <span v-if="p.required===1" style="color:#f56c6c">*</span>
            </el-form-item>
            <el-form-item>
              <el-button type="primary" :loading="querying" native-type="submit">查询</el-button>
              <el-button @click="resetForm">重置</el-button>
            </el-form-item>
          </el-form>
        </el-card>

        <!-- 表格右上角副功能区：刷新 / 列配置 / 隐藏搜索 / 导出（导出受报表 PubAllowExport 开关控制） -->
        <div class="table-toolbar">
          <el-tooltip content="刷新（更新当前页数据）" placement="top">
            <el-button circle :loading="querying" @click="doQuery(page)">
              <el-icon><i-refresh></i-refresh></el-icon>
            </el-button>
          </el-tooltip>
          <el-tooltip content="列配置（宽度 / 显隐 / 位置 / 冻结）" placement="top">
            <el-button circle @click="openCfgDlg">
              <el-icon><i-setting></i-setting></el-icon>
            </el-button>
          </el-tooltip>
          <el-tooltip :content="searchVisible ? '隐藏搜索' : '显示搜索'" placement="top">
            <el-button circle @click="searchVisible = !searchVisible">
              <el-icon>
                <i-hide v-if="searchVisible"></i-hide>
                <i-view v-else></i-view>
              </el-icon>
            </el-button>
          </el-tooltip>
          <el-tooltip v-if="info.export" content="导出" placement="top">
            <el-button circle type="primary" plain @click="exportDlg = true">
              <el-icon><i-download></i-download></el-icon>
            </el-button>
          </el-tooltip>
        </div>

        <!-- 结果表格：遮罩只覆盖这一块（含分页），带文字提示 -->
        <!-- table-wrap 吃掉剩余高度，el-table 用 :height 撑满 → 表头固定、表格内部滚动 -->
        <div class="table-wrap" ref="tableWrap" v-loading="loading || querying" element-loading-text="数据加载中...">
          <el-table :data="tableRows" border stripe size="small" :height="tableHeight"
                    @sort-change="onSortChange" @selection-change="onSelChange"
                    :empty-text="params.length ? '请设置查询条件后点击「查询」' : '暂无数据'">
            <el-table-column type="selection" width="42" fixed="left"></el-table-column>
            <el-table-column v-for="dc in displayCols" :key="dc.col.name"
                             :prop="'c'+dc.idx" :label="dc.col.label || dc.col.name"
                             :width="dc.width > 0 ? dc.width : colWidth(dc.col)"
                             :fixed="dc.fixed"
                             :show-overflow-tooltip="!denseMode"
                             :sortable="dc.col.name ? 'custom' : false">
              <template #header>
                <!-- 原生 title 代替 el-tooltip：宽表（上百列）时组件数量少一个量级，滚动不卡 -->
                <span class="th-ellipsis" :title="dc.col.label || dc.col.name" @click.stop>{{ dc.col.label || dc.col.name }}</span>
              </template>
              <template #default="s">{{ s.row['c'+dc.idx] === null ? '' : s.row['c'+dc.idx] }}</template>
            </el-table-column>
          </el-table>
        </div>

        <!-- 分页固定在页面底部：表格内部滚动时它始终可见 -->
        <div class="table-footer" v-if="pagerTotal > 0">
          <span v-if="!totalKnown" class="muted" style="margin-right:12px">总数统计超时，按当前页近似翻页</span>
          <el-pagination background layout="total, sizes, prev, pager, next"
                         :total="pagerTotal" :current-page="page" :page-size="pageSize"
                         :page-sizes="pageSizes"
                         @current-change="(p) => { doQuery(p) }"
                         @size-change="onSizeChange"></el-pagination>
        </div>

        <!-- 列配置弹窗：匿名用户共用一套配置 -->
        <!-- 性能关键：编辑只改草稿 cfgDraft，防抖后才同步主表格；列表用原生表格+原生控件，
             几百列时组件数比 el-table+el-switch 方案低两个数量级，弹窗内操作不再卡顿 -->
        <el-dialog v-model="cfgDlg" title="列配置" width="680px">
          <el-alert type="info" :closable="false" style="margin-bottom:10px"
                    title="调整实时生效（仅当前页面）；点「保存」后所有公开访问用户共用此配置；「重置」恢复初始"></el-alert>
          <div class="cfg-list">
            <table class="cfg-table">
              <thead>
                <tr><th class="tc">顺序</th><th>列名</th><th class="tc">列宽</th><th class="tc">显示</th><th class="tc">冻结</th></tr>
              </thead>
              <tbody>
                <tr v-for="(c, i) in cfgDraft" :key="c.name">
                  <td class="tc">
                    <button type="button" class="cfg-btn" :disabled="i === 0" title="上移"
                            @click="moveCol(i, -1)">↑</button>
                    <button type="button" class="cfg-btn" :disabled="i === cfgDraft.length - 1" title="下移"
                            @click="moveCol(i, 1)">↓</button>
                  </td>
                  <td class="cfg-name" :title="c.label">{{ c.label }}</td>
                  <td class="tc"><input type="number" class="cfg-num" min="0" max="1000"
                                        v-model.number="c.width" title="0 = 自动列宽"></td>
                  <td class="tc"><input type="checkbox" v-model="c.visible"></td>
                  <td class="tc"><input type="checkbox" v-model="c.frozen"></td>
                </tr>
              </tbody>
            </table>
          </div>
          <template #footer>
            <el-button @click="resetColCfg">重 置</el-button>
            <el-button @click="cfgDlg = false">关 闭</el-button>
            <el-button type="primary" :loading="cfgSaving" @click="saveColCfg">保 存</el-button>
          </template>
        </el-dialog>

        <!-- 导出弹窗：范围 + 格式 -->
        <el-dialog v-model="exportDlg" title="导出数据" width="500px">
          <el-form label-width="90px">
            <el-form-item label="导出范围">
              <el-radio-group v-model="exportScope" class="export-radios">
                <el-radio value="selected" :disabled="!selection.length">当前勾选行（{{ selection.length }} 行）</el-radio>
                <el-radio value="page">当前页（第 {{ page }} 页，共 {{ tableRows.length }} 行）</el-radio>
                <el-radio value="range">指定页</el-radio>
                <el-radio value="all">全部数据（共 {{ totalKnown ? (result.total || 0) : '未知' }} 行）</el-radio>
              </el-radio-group>
            </el-form-item>
            <el-form-item v-if="exportScope === 'range'" label="起始页码">
              <el-input-number v-model="rangePage" :min="1" :max="1000000" style="width:150px"></el-input-number>
            </el-form-item>
            <el-form-item v-if="exportScope === 'range'" label="导出条数">
              <el-input-number v-model="rangeCount" :min="1" :max="200000" style="width:150px"></el-input-number>
            </el-form-item>
            <el-form-item label="文件格式">
              <el-radio-group v-model="exportFormat">
                <el-radio value="xlsx">Excel (.xlsx)</el-radio>
                <el-radio value="csv">CSV (.csv)</el-radio>
              </el-radio-group>
            </el-form-item>
          </el-form>
          <template #footer>
            <el-button @click="exportDlg = false">取消</el-button>
            <el-button type="primary" :loading="!!exporting" @click="doScopedExport">导 出</el-button>
          </template>
        </el-dialog>
      </template>
    </div>
  </div>
</template>

<script>
import { ElMessageBox, ElMessage } from 'element-plus'
import { RS } from '@/lib/rs.js'
import tableFill from '@/lib/tableFill.js'

export default {
  mixins: [tableFill],
  data: function () {
    return {
      pid: '', token: '',
      info: {}, params: [], form: {}, options: {}, optionLoading: {},
      result: {}, page: 1, pageSize: 20, sortCol: '', sortDir: '',
      loading: false, querying: false, denied: false, exporting: '',
      // 工具条：搜索显隐 / 勾选行 / 列配置
      searchVisible: true,
      selection: [],
      colCfg: [], cfgDlg: false, cfgSaving: false, pendingCfg: null,
      // 列配置弹窗草稿：编辑先落在草稿上，防抖后再同步到 colCfg（避免每敲一键就全量重渲染主表格）
      cfgDraft: [], cfgTimer: null,
      // 导出
      exportDlg: false, exportScope: 'all', exportFormat: 'xlsx',
      rangePage: 1, rangeCount: 100
    };
  },
  computed: {
    tableRows: function () { return (this.result.rows || []).map(function (r) {
      var o = {}; r.forEach(function (v, i) { o['c' + i] = v; }); return o; }); },
    /**
     * 实际渲染列：按列配置的顺序/显隐/宽度/冻结应用；
     * 配置里没有的列（如结果列变化后新增的）自动按原始顺序补到末尾
     */
    displayCols: function () {
      var cols = this.result.columns || [];
      if (!cols.length) return [];
      var byName = {};
      cols.forEach(function (c, i) { byName[c.name] = i; });
      var used = {}, out = [];
      this.colCfg.forEach(function (e) {
        var i = byName[e.name];
        if (i === undefined || used[e.name]) return;
        used[e.name] = true;
        if (!e.visible) return;
        out.push({ col: cols[i], idx: i, width: e.width > 0 ? e.width : 0, fixed: !!e.frozen });
      });
      cols.forEach(function (c, i) {
        if (!used[c.name]) out.push({ col: c, idx: i, width: 0, fixed: false });
      });
      return out;
    },
    /**
     * 宽表降级：列数多时关闭单元格 show-overflow-tooltip。
     * 每个可溢出单元格都会挂一个浮层组件，上百列 × 200 行是渲染卡死的主因；
     * 表头仍保留原生 title，看全称不受影响，只是不再有悬浮框。
     */
    denseMode: function () { return this.displayCols.length > 30; },
    /** 宽表下不再提供 200 行/页，避免一次渲染几万个单元格把主线程堵死 */
    pageSizes: function () { return this.denseMode ? [20, 50, 100] : [20, 50, 100, 200]; },
    /** 后端为 -1 表示「总数统计超时，未知」，不是 0 行 */
    totalKnown: function () { return (this.result.total === undefined || this.result.total >= 0); },
    /**
     * 分页器用的总数：总数已知时直接用；未知时退化为「当前页之前 + 本页 + 是否还有下一页」，
     * 让「下一页」仍然可用，只是总条数显示为近似值
     */
    pagerTotal: function () {
      var t = this.result.total;
      if (t === undefined || t >= 0) return t || 0;
      var n = this.tableRows.length;
      return (this.page - 1) * this.pageSize + n + (n === this.pageSize ? 1 : 0);
    }
  },
  watch: {
    /** 草稿编辑防抖同步：连续输入/开关在 200ms 内合并为一次主表格重渲染 */
    cfgDraft: {
      deep: true,
      handler: function () {
        var self = this;
        if (this.cfgTimer) clearTimeout(this.cfgTimer);
        this.cfgTimer = setTimeout(function () {
          self.cfgTimer = null;
          self.applyDraft();
        }, 200);
      }
    }
  },
  beforeUnmount: function () { if (this.cfgTimer) clearTimeout(this.cfgTimer); },
  created: function () {
    // URL: /r/{publicId}?token=xxx
    var parts = location.pathname.split('/').filter(Boolean);
    this.pid = parts.length >= 2 ? parts[1] : '';
    this.token = RS.qs('token') || RS.qs('t');
    if (!this.pid || !this.token) { this.denied = true; this.loading = false; return; }
    this.load();
  },
  methods: {
    /**
     * 列宽策略：人工配置（展示列 width>0）优先；未配置时默认 100，
     * 标题过长按字数估算自动加宽，上限 200
     * @param {Object} c 结果列对象 { name, label, width }
     */
    colWidth: function (c) {
      if (c.width && c.width > 0) return c.width;
      var t = c.label || c.name || '';
      var w = 0;
      for (var i = 0; i < t.length; i++) w += t.charCodeAt(i) > 255 ? 14 : 8; // 中文14px/英文8px
      return Math.min(200, Math.max(100, w + 26)); // 26 = 单元格左右内边距
    },
    load: function () {
      var self = this;
      this.loading = true;
      RS.pubApi(this.pid, this.token, 'GET', '/info')
        .then(function (info) {
          self.info = info || {};
          // 条件配置由 /info 脱敏返回（不含 optionSql），必须落到 params 上才会渲染查询区
          self.params = self.info.params || [];
          self.initDefaults();
          // 下拉条件进入页面即预载选项（SQL/字典来源），默认值立刻以标签回显
          self.params.forEach(function (p) { self.loadOptions(p); });
          // 先取列配置再首查，保证首次渲染即应用已保存配置；
          // 必填条件无默认值时无法直接查询，等用户填写后再由「查询」触发
          self.loadColCfg(function () {
            self.loading = false;
            if (!self.missingRequired()) self.doQuery(1);
          });
        })
        .catch(function () { self.denied = true; self.loading = false; });
    },
    /**
     * 初始化条件默认值（复刻后端 resolveDefault 的宏逻辑，仅用于表单回显；
     * 本地时区格式化，避免 toISOString 按 UTC 换算导致东八区日期偏移一天）
     */
    initDefaults: function () {
      var pad = function (n) { return (n < 10 ? '0' : '') + n; };
      var fmt = function (d) { return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()); };
      var ago = function (off) { var t = new Date(); t.setDate(t.getDate() - off); return fmt(t); };
      var f = {};
      this.params.forEach(function (p) {
        var macro = p.defaultMacro || '';
        if (!macro && p.defaultVal && p.defaultVal.indexOf('${') === 0) {
          macro = p.defaultVal.slice(2, -1);
        }
        if (macro) {
          if (macro === 'today') f[p.paramName] = p.widget === 'dateRange' ? [ago(0), ago(0)] : ago(0);
          else if (macro === 'yesterday') f[p.paramName] = p.widget === 'dateRange' ? [ago(1), ago(1)] : ago(1);
          else if (macro === 'last7days') f[p.paramName] = [ago(6), ago(0)];
          else if (macro === 'last30days') f[p.paramName] = [ago(29), ago(0)];
          else if (macro === 'thisMonth') {
            var ms = new Date(); ms.setDate(1);
            f[p.paramName] = [fmt(ms), ago(0)];
          }
          return;
        }
        if (p.defaultVal && p.defaultVal.indexOf('${') !== 0) {
          f[p.paramName] = p.widget === 'multiSelect' ? String(p.defaultVal).split(',') : p.defaultVal;
        }
      });
      this.form = f;
      this.options = {};
    },
    fixedOptions: function (p) {
      if (p.optionType !== 'fixed' || !p.optionFixed) return [];
      try { return JSON.parse(p.optionFixed); } catch (e) { return []; }
    },
    /**
     * 组装查询参数：文本/数字控件支持多行多值搜索——
     * 按换行/逗号/分号拆分、去空白：0 个 → 空串；1 个 → 原字符串；多个 → 数组（后端按 IN 展开）
     */
    buildQueryParams: function () {
      var self = this;
      var out = Object.assign({}, this.form);
      this.params.forEach(function (p) {
        var v = out[p.paramName];
        if ((p.widget === 'text' || p.widget === 'number') && typeof v === 'string') {
          var arr = String(v).split(/[\n\r,，;；]+/)
            .map(function (s) { return s.trim(); })
            .filter(function (s) { return s !== ''; });
          // 拆完一个值都不剩：说明用户填过又删光了，置 null（按“未填”处理）。
          // 若发空字符串，后端会绑定成 col = '' 导致查不出任何数据
          out[p.paramName] = arr.length === 0 ? null : (arr.length === 1 ? arr[0] : arr);
        }
      });
      return out;
    },
    /**
     * 加载 SQL / 字典类条件的下拉选项。
     * 公开接口不下发 optionSql / optionDict（防泄漏），选项统一由服务端 /options 执行，
     * 因此这里只按 optionType 判断，不再依赖前端拿到的配置内容。
     */
    loadOptions: function (p) {
      if (p.optionType !== 'sql' && p.optionType !== 'dict') return;
      if (this.optionLoading[p.paramName]) return;
      var self = this;
      this.optionLoading[p.paramName] = true;
      RS.pubApi(this.pid, this.token, 'POST', '/options', { param: p.paramName, params: this.buildQueryParams() })
        .then(function (rows) { self.options[p.paramName] = rows || []; })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.optionLoading[p.paramName] = false; });
    },
    /** 是否存在未填写的必填条件 */
    missingRequired: function () {
      var self = this;
      return this.params.some(function (p) {
        var v = self.form[p.paramName];
        return p.required === 1 && (v === undefined || v === null || v === '' ||
          (Array.isArray(v) && !v.length));
      });
    },
    /** 必填校验（与登录后的报表页保持一致，避免只依赖后端报错）；基于拆分后的真实值校验 */
    validateRequired: function () {
      var q = this.buildQueryParams();
      for (var i = 0; i < this.params.length; i++) {
        var p = this.params[i];
        var v = q[p.paramName];
        if (p.required === 1 && (v === undefined || v === null || v === '' ||
            (Array.isArray(v) && !v.length))) {
          ElMessage.warning('请填写「' + p.label + '」');
          return false;
        }
      }
      return true;
    },
    doQuery: function (page) {
      if (!this.validateRequired()) return;
      // 查询未返回前不再发起新查询：Oracle 大表查询本就慢，连点只会排队把数据库压满
      if (this.querying) return;
      var self = this;
      this.page = page || 1;
      this.selection = []; // 换页/重查后勾选失效
      this.querying = true;
      RS.pubApi(this.pid, this.token, 'POST', '/query', {
        params: this.buildQueryParams(), page: this.page, pageSize: this.pageSize,
        sortCol: this.sortCol, sortDir: this.sortDir
      }).then(function (data) {
        self.result = data || {};
        // 首次拿到结果列时应用已保存的列配置，其后仅做增量对齐
        if (self.pendingCfg) { self.applyCfg(self.pendingCfg); self.pendingCfg = null; }
        else self.syncCfg();
      }).catch(function (e) { RS.alertErr(e); })
        .finally(function () {
          // 等这次（可能很慢的）表格渲染落地后再撤遮罩：
          // 若和 result 赋值放在同一次渲染里，遮罩会先消失、表格才开始渲染，用户看到的是「无提示卡死」
          self.$nextTick(function () { self.querying = false; });
        });
    },
    resetForm: function () { this.initDefaults(); this.result = {}; },
    onSizeChange: function (n) { this.pageSize = n; this.doQuery(1); },
    onSortChange: function (s) {
      if (!s.prop) { this.sortCol = ''; this.sortDir = ''; }
      else {
        this.sortCol = (this.result.columns || [])[parseInt(s.prop.slice(1), 10)].name;
        this.sortDir = s.order === 'descending' ? 'desc' : 'asc';
      }
      this.doQuery(1);
    },
    // ---------- 工具条：勾选 ----------
    onSelChange: function (rows) { this.selection = rows; },
    // ---------- 工具条：列配置（匿名共用一套） ----------
    /**
     * 让 colCfg 与结果列对齐：保留已有配置的顺序与设置，
     * 补充结果中新出现的列（补到末尾）、剔除已不存在的列、刷新标题
     */
    syncCfg: function () {
      var cols = this.result.columns || [];
      var out = [];
      for (var i = 0; i < this.colCfg.length; i++) {
        var e = this.colCfg[i];
        for (var j = 0; j < cols.length; j++) {
          if (cols[j].name === e.name) {
            e.label = cols[j].label || cols[j].name;
            out.push(e);
            break;
          }
        }
      }
      for (var k = 0; k < cols.length; k++) {
        var found = false;
        for (var m = 0; m < out.length; m++) {
          if (out[m].name === cols[k].name) { found = true; break; }
        }
        if (!found) out.push({ name: cols[k].name, label: cols[k].label || cols[k].name,
          width: 0, visible: true, frozen: false });
      }
      // 与现有配置逐项比较，完全一致则不替换：换新数组会让 displayCols 重新计算，
      // 触发主表格整体重渲染（宽表时很贵），打开弹窗/重复查询不应白付这笔开销
      var cur = this.colCfg;
      if (cur.length === out.length) {
        var same = true;
        for (var n = 0; n < out.length; n++) {
          if (cur[n].name !== out[n].name || cur[n].label !== out[n].label ||
              cur[n].width !== out[n].width || cur[n].visible !== out[n].visible ||
              cur[n].frozen !== out[n].frozen) { same = false; break; }
        }
        if (same) return;
      }
      this.colCfg = out;
    },
    /** 应用已保存的配置：按保存的顺序生效，结果列里新增的自动补尾 */
    applyCfg: function (saved) {
      var cols = this.result.columns || [];
      var byName = {};
      cols.forEach(function (c) { byName[c.name] = c; });
      var out = [];
      (saved || []).forEach(function (e) {
        if (e && byName[e.name]) {
          out.push({ name: e.name, label: byName[e.name].label || e.name,
            width: parseInt(e.width, 10) || 0, visible: e.visible !== false, frozen: !!e.frozen });
        }
      });
      this.colCfg = out;
      this.syncCfg();
    },
    /** 载入匿名列配置（所有公开访问用户共用一套）；done 为可选回调 */
    loadColCfg: function (done) {
      var self = this;
      RS.pubApi(this.pid, this.token, 'GET', '/column-config').then(function (data) {
        var saved = null;
        if (data && data.config) {
          try { saved = JSON.parse(data.config); } catch (e) { /* 配置损坏按无配置处理 */ }
        }
        if ((self.result.columns || []).length) self.applyCfg(saved || []);
        else self.pendingCfg = saved;
      }).catch(function () { /* 配置读取失败不阻塞报表使用 */ })
        .finally(function () { if (done) done(); });
    },
    /** 打开弹窗：以当前生效配置为底稿复制一份草稿，编辑先落在草稿上（与主表格渲染解耦） */
    openCfgDlg: function () {
      this.syncCfg();
      this.cfgDraft = this.colCfg.map(function (e) {
        return { name: e.name, label: e.label, width: e.width, visible: e.visible, frozen: e.frozen };
      });
      this.cfgDlg = true;
    },
    /** 上下移只动草稿，由 cfgDraft 的防抖监听统一同步到主表格（连点自动合并） */
    moveCol: function (i, dir) {
      var arr = this.cfgDraft;
      var t = arr[i]; arr[i] = arr[i + dir]; arr[i + dir] = t;
    },
    /** 把草稿应用到底层 colCfg；与当前生效配置一致时不触发主表格重渲染 */
    applyDraft: function () {
      var draft = this.cfgDraft, cur = this.colCfg;
      if (cur.length === draft.length) {
        var same = true;
        for (var i = 0; i < draft.length; i++) {
          if (cur[i].name !== draft[i].name || cur[i].label !== draft[i].label ||
              cur[i].width !== (draft[i].width > 0 ? draft[i].width : 0) ||
              cur[i].visible !== (draft[i].visible !== false) ||
              cur[i].frozen !== !!draft[i].frozen) { same = false; break; }
        }
        if (same) return;
      }
      this.colCfg = draft.map(function (e) {
        return { name: e.name, label: e.label, width: e.width > 0 ? e.width : 0,
          visible: e.visible !== false, frozen: !!e.frozen };
      });
    },
    /** 立即同步尚未落地的防抖（保存前调用，保证保存的就是所见） */
    flushDraft: function () {
      if (this.cfgTimer) { clearTimeout(this.cfgTimer); this.cfgTimer = null; this.applyDraft(); }
    },
    saveColCfg: function () {
      var self = this;
      this.flushDraft();
      this.cfgSaving = true;
      RS.pubApi(this.pid, this.token, 'PUT', '/column-config', {
        config: this.cfgDraft.map(function (e) {
          return { name: e.name, width: parseInt(e.width, 10) || 0,
            visible: e.visible !== false, frozen: !!e.frozen };
        })
      }).then(function () {
        ElMessage.success('列配置已保存，所有公开访问用户生效');
        self.cfgDlg = false;
      }).catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.cfgSaving = false; });
    },
    /** 重置：恢复初始列形式并清除共享配置（立即生效且长期生效） */
    resetColCfg: function () {
      var self = this;
      ElMessageBox.confirm('将恢复初始列形式，并清除当前共享的自定义配置，确定？', '重置列配置',
        { type: 'warning', confirmButtonText: '确定', cancelButtonText: '取消' })
        .then(function () {
          if (self.cfgTimer) { clearTimeout(self.cfgTimer); self.cfgTimer = null; }
          self.cfgDraft = [];
          self.colCfg = [];
          self.syncCfg();
          self.cfgDraft = self.colCfg.map(function (e) {
            return { name: e.name, label: e.label, width: e.width, visible: e.visible, frozen: e.frozen };
          });
          return RS.pubApi(self.pid, self.token, 'DELETE', '/column-config');
        })
        .then(function () { ElMessage.success('已重置为初始列配置'); })
        .catch(function (e) { if (e && e.message) RS.alertErr(e); });
    },
    // ---------- 工具条：导出 ----------
    /**
     * 按弹窗选择的范围导出（公开访问仅同步流式，无异步任务）：
     * selected=勾选行（前端回传数据） / page=当前页 / range=指定页 / all=全部
     */
    doScopedExport: function () {
      var self = this;
      var fmt = this.exportFormat;
      var body;
      if (this.exportScope === 'selected') {
        if (!this.selection.length) { ElMessage.warning('请先勾选要导出的行'); return; }
        body = {
          format: fmt, scope: 'rows',
          columns: this.displayCols.map(function (dc) { return dc.col.label || dc.col.name; }),
          rows: this.selection.map(function (r) {
            return self.displayCols.map(function (dc) { return r['c' + dc.idx]; });
          })
        };
      } else if (this.exportScope === 'page') {
        body = { format: fmt, scope: 'page', params: this.buildQueryParams(),
          sortCol: this.sortCol, sortDir: this.sortDir,
          page: this.page, pageSize: this.pageSize };
      } else if (this.exportScope === 'range') {
        body = { format: fmt, scope: 'page', params: this.buildQueryParams(),
          sortCol: this.sortCol, sortDir: this.sortDir,
          page: this.rangePage || 1, pageSize: this.rangeCount || 100 };
      } else {
        body = { format: fmt, params: this.buildQueryParams(), sortCol: this.sortCol, sortDir: this.sortDir };
      }
      this.exporting = fmt;
      fetch('/api/pub/' + this.pid + '/export', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Report-Token': this.token },
        body: JSON.stringify(body)
      }).then(function (res) { return self.handleExportRes(res, fmt); })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.exporting = ''; });
    },
    /** 导出响应处理：JSON=错误信息，二进制=同步下载 */
    handleExportRes: function (res, fmt) {
      var ct = res.headers.get('Content-Type') || '';
      if (ct.indexOf('application/json') >= 0) {
        return res.json().then(function (j) {
          throw new Error(j.msg || '导出失败');
        });
      }
      if (!res.ok) throw new Error('导出失败(HTTP ' + res.status + ')');
      return res.blob().then(function (b) {
        var cd = res.headers.get('Content-Disposition') || '';
        var m = /filename\*=UTF-8''([^;]+)/.exec(cd);
        var name = m ? decodeURIComponent(m[1]) : ('export.' + fmt);
        var url = URL.createObjectURL(b);
        var a = document.createElement('a');
        a.href = url; a.download = name;
        document.body.appendChild(a); a.click(); a.remove();
        URL.revokeObjectURL(url);
        ElMessage.success('导出完成：' + name);
      });
    }
  }
}
</script>

<style scoped>
/* 列配置弹窗：原生轻量列表，几百列时依然流畅（替代 el-table + 组件化控件） */
.cfg-list {
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 4px;
  max-height: 420px;
  overflow: auto;
}
.cfg-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.cfg-table thead th {
  position: sticky; top: 0; z-index: 1;
  background: var(--el-fill-color-light);
  color: var(--el-text-color-secondary);
  font-weight: 600;
  padding: 6px 10px;
  text-align: left;
  border-bottom: 1px solid var(--el-border-color-lighter);
}
.cfg-table tbody td { padding: 4px 10px; border-bottom: 1px solid var(--el-border-color-lighter); }
.cfg-table tbody tr:hover td { background: var(--el-fill-color-lighter); }
.cfg-table .tc { text-align: center; white-space: nowrap; }
.cfg-table .cfg-name { max-width: 260px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cfg-num {
  width: 90px; padding: 3px 6px;
  border: 1px solid var(--el-border-color); border-radius: 4px; outline: none;
  color: var(--el-text-color-regular); background: var(--el-fill-color-blank);
}
.cfg-num:focus { border-color: var(--el-color-primary); }
.cfg-btn {
  min-width: 26px; height: 22px; margin: 0 2px; padding: 0;
  border: 1px solid var(--el-border-color); border-radius: 4px;
  background: var(--el-fill-color-blank); cursor: pointer; line-height: 20px;
  color: var(--el-text-color-regular);
}
.cfg-btn:hover:not(:disabled) { color: var(--el-color-primary); border-color: var(--el-color-primary); }
.cfg-btn:disabled { opacity: .35; cursor: not-allowed; }
</style>
