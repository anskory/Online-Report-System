<!--
  SqlEditor.vue - 轻量 SQL 编辑器（零外部依赖）
  ─────────────────────────────────────────────────────────────
  能力：语法高亮（关键字/函数/字符串/注释/数字/:参数）、补全提示（关键字 + 表名 + 字段名 +
  已配置条件参数）、一键格式化（关键字大写 + 子句换行）、行号。

  实现方式：一个透明文字的 textarea 叠在着色的 <pre> 上，两层字体度量由同一套 CSS 保证
  完全一致（等宽字体 + 同样的 padding / line-height + 关闭软换行），所以光标、选区、滚动
  天然对齐。刻意不引入 CodeMirror / Monaco：本系统是离线内网部署，多一个包就多一次
  npm install 失败的可能，而这里需要的只是「看得清 + 补得全 + 能格式化」。

  对齐关键：textarea 必须 wrap="off"（不软换行），pre 用 white-space:pre，
  这样横向滚动也能同步，长 SQL 行不会因为自动折行错位。
-->
<template>
  <div class="sql-editor" :style="{ height: height + 'px' }">
    <!-- 工具条 -->
    <div class="sql-bar">
      <el-button size="small" text type="primary" @click="doFormat">
        <el-icon style="margin-right:3px"><MagicStick /></el-icon>格式化
      </el-button>
      <el-button size="small" text @click="openTip(true)">
        <el-icon style="margin-right:3px"><Mouse /></el-icon>提示
      </el-button>
      <slot name="extra"></slot>
      <span class="sql-bar-hint">Ctrl+空格 提示 · Ctrl+回车 格式化</span>
    </div>

    <div class="sql-body" ref="body">
      <!-- 行号：随 textarea 纵向滚动同步 -->
      <div class="sql-gutter" ref="gutter">
        <div v-for="n in lineCount" :key="n">{{ n }}</div>
      </div>

      <div class="sql-pane">
        <!-- 着色层（不接收事件） -->
        <pre class="sql-hl" ref="hl" aria-hidden="true"><code v-html="html"></code></pre>
        <!-- 输入层：文字透明、光标可见 -->
        <textarea ref="ta" class="sql-ta" :value="modelValue" :placeholder="placeholder"
                  spellcheck="false" wrap="off"
                  @input="onInput" @scroll="syncScroll" @keydown="onKeyDown"
                  @blur="closeTip" @click="closeTip"></textarea>
      </div>

      <!-- 补全浮层 -->
      <ul class="sql-tip" v-if="tip.open" :style="tipStyle">
        <li v-for="(c, i) in tip.items" :key="c.label + '|' + i"
            :class="{ 'is-active': i === tip.idx }"
            @mousedown.prevent="acceptTip(c)">
          <span class="sql-tip-label">{{ c.label }}</span>
          <span class="sql-tip-kind">{{ c.kind }}</span>
        </li>
        <li v-if="!tip.items.length" class="sql-tip-empty">没有匹配项</li>
      </ul>
    </div>
  </div>
</template>

<script>
import { MagicStick, Mouse } from '@element-plus/icons-vue'

/** SQL 关键字（参与高亮与补全；刻意不含 DATE/VALUE/NUMBER 这类常被用作列名的词） */
const KEYWORDS = ('SELECT,FROM,WHERE,AND,OR,NOT,IN,IS,NULL,LIKE,BETWEEN,GROUP,BY,ORDER,HAVING,' +
  'LIMIT,OFFSET,AS,ON,JOIN,LEFT,RIGHT,INNER,OUTER,FULL,CROSS,UNION,ALL,DISTINCT,CASE,WHEN,' +
  'THEN,ELSE,END,INSERT,UPDATE,DELETE,VALUES,INTO,EXISTS,ASC,DESC,WITH,OVER,PARTITION,' +
  'ROWNUM,FETCH,NEXT,ROWS,ONLY,TOP,DUAL,USING,NATURAL').split(',')

/** 常用函数（只做着色，避免把业务列名误判为关键字） */
const FUNCS = ('COUNT,SUM,AVG,MIN,MAX,ROUND,TRUNC,NVL,NVL2,COALESCE,DECODE,TO_CHAR,TO_DATE,' +
  'TO_NUMBER,SUBSTR,TRIM,UPPER,LOWER,LENGTH,ABS,CEIL,FLOOR,CAST,CONVERT,ROW_NUMBER,RANK,' +
  'DENSE_RANK,LISTAGG,WM_CONCAT,DATEADD,DATEDIFF,IFNULL,NOW,GETDATE').split(',')

const KW_SET = {}
KEYWORDS.forEach((k) => { KW_SET[k] = 1 })
const FN_SET = {}
FUNCS.forEach((k) => { FN_SET[k] = 1 })

/** 行号栏宽度 + 编辑器内边距（与 style.css 里 .sql-* 的数值保持一致） */
const GUTTER_W = 46
const PAD = 8

/** HTML 转义：高亮层用 v-html，任何原文都必须先转义 */
function esc(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;')
    .replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}

function span(cls, text) {
  return '<span class="sql-' + cls + '">' + esc(text) + '</span>'
}

/**
 * 把 SQL 源码切分并着色。
 * 逐字符扫描（不是一遍正则），保证字符串/注释里的关键字不会被误染。
 * @param {string} src SQL 源码
 * @returns {string} 可直接 v-html 的 HTML
 */
function highlight(src) {
  var s = String(src || '')
  var out = ''
  var i = 0
  while (i < s.length) {
    var ch = s[i]
    var j
    // 行注释 -- ...
    if (ch === '-' && s[i + 1] === '-') {
      j = s.indexOf('\n', i)
      if (j < 0) j = s.length
      out += span('cmt', s.slice(i, j))
      i = j
      continue
    }
    // 块注释 /* ... */
    if (ch === '/' && s[i + 1] === '*') {
      j = s.indexOf('*/', i + 2)
      j = j < 0 ? s.length : j + 2
      out += span('cmt', s.slice(i, j))
      i = j
      continue
    }
    // 单引号字符串（'' 为转义）
    if (ch === "'") {
      j = i + 1
      while (j < s.length) {
        if (s[j] === "'" && s[j + 1] === "'") { j += 2; continue }
        if (s[j] === "'") { j += 1; break }
        j += 1
      }
      out += span('str', s.slice(i, j))
      i = j
      continue
    }
    // 双引号/反引号标识符
    if (ch === '"' || ch === '`') {
      j = s.indexOf(ch, i + 1)
      j = j < 0 ? s.length : j + 1
      out += span('ident', s.slice(i, j))
      i = j
      continue
    }
    // 条件占位符 :name（本系统的参数绑定语法，单独着色便于一眼看出）
    if (ch === ':') {
      var pm = /^:[A-Za-z_][A-Za-z0-9_]*/.exec(s.slice(i))
      if (pm) {
        out += span('param', pm[0])
        i += pm[0].length
        continue
      }
    }
    // 数字
    if (ch >= '0' && ch <= '9') {
      var nm = /^[0-9]+(\.[0-9]+)?/.exec(s.slice(i))
      out += span('num', nm[0])
      i += nm[0].length
      continue
    }
    // 单词：关键字 / 函数 / 普通标识符
    if (/[A-Za-z_]/.test(ch)) {
      var wm = /^[A-Za-z_][A-Za-z0-9_$#]*/.exec(s.slice(i))
      var w = wm[0]
      var up = w.toUpperCase()
      if (KW_SET[up]) out += span('kw', w)
      else if (FN_SET[up]) out += span('fn', w)
      else out += esc(w)
      i += w.length
      continue
    }
    out += esc(ch)
    i += 1
  }
  // 末尾补一个换行：最后一行是空行时，着色层与输入层高度才不会差一行
  return out + '\n'
}

/**
 * 把 SQL 拆成「代码段 / 非代码段（字符串、注释）」，格式化只动代码段，
 * 字符串与注释原样保留。
 * @param {string} src SQL 源码
 * @returns {Array<{code:boolean,text:string}>}
 */
function splitParts(src) {
  var s = String(src || '')
  var parts = []
  var buf = ''
  var i = 0
  var flush = function () { if (buf) { parts.push({ code: true, text: buf }); buf = '' } }
  while (i < s.length) {
    var ch = s[i]
    var j
    if (ch === '-' && s[i + 1] === '-') {
      flush()
      j = s.indexOf('\n', i)
      if (j < 0) j = s.length
      parts.push({ code: false, text: s.slice(i, j) })
      i = j
      continue
    }
    if (ch === '/' && s[i + 1] === '*') {
      flush()
      j = s.indexOf('*/', i + 2)
      j = j < 0 ? s.length : j + 2
      parts.push({ code: false, text: s.slice(i, j) })
      i = j
      continue
    }
    if (ch === "'" || ch === '"' || ch === '`') {
      flush()
      j = i + 1
      while (j < s.length) {
        if (s[j] === ch && s[j + 1] === ch) { j += 2; continue }
        if (s[j] === ch) { j += 1; break }
        j += 1
      }
      parts.push({ code: false, text: s.slice(i, j) })
      i = j
      continue
    }
    buf += ch
    i += 1
  }
  flush()
  return parts
}

/** 代码段格式化：压缩空白 → 关键字大写 → 主要子句换行 → AND/OR/ON 缩进 */
function formatCode(code) {
  var t = code.replace(/\s+/g, ' ')
  // 两词/三词关键字先合并，避免换行时被拆成两行
  t = t.replace(/\b(GROUP|ORDER)\s+BY\b/gi, function (m) { return m.toUpperCase().replace(/\s+/g, ' ') })
  t = t.replace(/\bUNION\s+ALL\b/gi, 'UNION ALL')
  t = t.replace(/\b(LEFT|RIGHT|INNER|FULL|CROSS)\s+(OUTER\s+)?JOIN\b/gi, function (m) {
    return m.toUpperCase().replace(/\s+/g, ' ')
  })
  // 关键字大写（表名/列名不在关键字表里，保持原样）
  t = t.replace(/[A-Za-z_][A-Za-z0-9_$#]*/g, function (w) {
    return KW_SET[w.toUpperCase()] ? w.toUpperCase() : w
  })
  // 主要子句各占一行
  t = t.replace(/\s*\b(SELECT|FROM|WHERE|GROUP BY|ORDER BY|HAVING|UNION ALL|UNION|LEFT JOIN|RIGHT JOIN|INNER JOIN|FULL JOIN|CROSS JOIN|JOIN)\b/g, '\n$1')
  // 连接条件与布尔运算缩进
  t = t.replace(/\s+\b(AND|OR)\b/g, '\n  $1')
  t = t.replace(/\s+\bON\b/g, '\n    ON')
  return t
}

/**
 * 格式化整段 SQL（字符串与注释原样保留）
 * @param {string} src SQL 源码
 * @returns {string}
 */
function formatSQL(src) {
  var parts = splitParts(src)
  var out = ''
  for (var k = 0; k < parts.length; k++) {
    var piece = parts[k].code ? formatCode(parts[k].text) : parts[k].text
    if (!piece) continue
    // 拼接处避免出现两个空格（只在代码段边界处理，不碰字符串内容）
    if (out && out.charAt(out.length - 1) === ' ' && piece.charAt(0) === ' ') piece = piece.slice(1)
    out += piece
  }
  return out.replace(/[ \t]+$/gm, '').replace(/\n{3,}/g, '\n\n').replace(/^\n+/, '')
}

export default {
  name: 'SqlEditor',
  components: { MagicStick, Mouse },
  props: {
    modelValue: { type: String, default: '' },
    /** 编辑器总高度（含工具条），px */
    height: { type: Number, default: 320 },
    placeholder: { type: String, default: '' },
    /** 补全用：当前数据源的表名清单 */
    tables: { type: Array, default: function () { return [] } },
    /** 补全用：当前表的字段名清单 */
    columns: { type: Array, default: function () { return [] } },
    /** 补全用：已配置的查询条件参数名（补全成 :name） */
    params: { type: Array, default: function () { return [] } }
  },
  data: function () {
    return {
      lineCount: 1,
      charW: 7.2,
      lineH: 20,
      tip: { open: false, items: [], idx: 0, x: 0, y: 0 }
    }
  },
  computed: {
    html: function () { return highlight(this.modelValue) },
    tipStyle: function () {
      return { left: this.tip.x + 'px', top: this.tip.y + 'px' }
    }
  },
  watch: {
    modelValue: function (v) {
      this.lineCount = String(v || '').split('\n').length
    }
  },
  mounted: function () {
    this.lineCount = String(this.modelValue || '').split('\n').length
    this.measure()
  },
  methods: {
    /** 量一次字符宽度与行高：浮层定位按等宽字符推算，比插一个隐藏 div 量光标便宜得多 */
    measure: function () {
      var ta = this.$refs.ta
      if (!ta) return
      var cs = window.getComputedStyle(ta)
      // getComputedStyle().font 在部分浏览器上返回空串，拼一个等价 font 简写兜底
      var font = cs.font && cs.font !== '' ? cs.font
        : (cs.fontStyle + ' ' + cs.fontWeight + ' ' + cs.fontSize + ' ' + cs.fontFamily)
      var probe = document.createElement('span')
      probe.style.cssText = 'position:absolute;top:-9999px;left:-9999px;visibility:hidden;' +
        'white-space:pre;font:' + font
      probe.textContent = 'M'.repeat(50)
      document.body.appendChild(probe)
      var w = probe.getBoundingClientRect().width / 50
      document.body.removeChild(probe)
      if (w > 0) this.charW = w
      var lh = parseFloat(cs.lineHeight)
      if (lh > 0) this.lineH = lh
    },
    onInput: function (e) {
      var v = e.target.value
      this.$emit('update:modelValue', v)
      this.lineCount = v.split('\n').length
      if (this.tip.open) {
        this.refreshItems()
      } else {
        var w = this.wordAt()
        if (w && w.text.length >= 2) this.openTip(false)
      }
    },
    /** 着色层与行号跟随输入层滚动 */
    syncScroll: function () {
      var ta = this.$refs.ta
      var hl = this.$refs.hl
      if (ta && hl) {
        hl.scrollTop = ta.scrollTop
        hl.scrollLeft = ta.scrollLeft
      }
      if (ta && this.$refs.gutter) this.$refs.gutter.scrollTop = ta.scrollTop
      this.closeTip()
    },
    /** 光标前的「当前单词」范围（含 : 前缀，便于补全参数） */
    wordAt: function () {
      var ta = this.$refs.ta
      if (!ta) return null
      var pos = ta.selectionStart
      if (pos == null) return null
      var v = ta.value
      var start = pos
      while (start > 0 && /[A-Za-z0-9_:$#]/.test(v.charAt(start - 1))) start -= 1
      return { start: start, end: pos, text: v.slice(start, pos) }
    },
    /** 组装候选列表：参数 → 表 → 字段 → 关键字 → 函数 */
    buildItems: function (prefix) {
      var p = String(prefix || '').replace(/^:/, '').toUpperCase()
      var items = []
      var push = function (label, kind, insert) {
        if (p && label.toUpperCase().indexOf(p) !== 0) return
        items.push({ label: label, kind: kind, insert: insert || label })
      }
      // 前缀带冒号时只补参数，避免把关键字混进来
      if (String(prefix || '').charAt(0) === ':') {
        this.params.forEach(function (n) { push(':' + n, '条件参数', ':' + n) })
        return items.slice(0, 60)
      }
      this.params.forEach(function (n) { push(':' + n, '条件参数', ':' + n) })
      this.tables.forEach(function (n) { push(n, '表') })
      this.columns.forEach(function (n) { push(n, '字段') })
      KEYWORDS.forEach(function (n) { push(n, '关键字') })
      FUNCS.forEach(function (n) { push(n, '函数') })
      return items.slice(0, 60)
    },
    /**
     * 打开/刷新补全浮层
     * @param {boolean} force 快捷键触发时忽略「至少 2 个字符」的限制
     */
    openTip: function (force) {
      var ta = this.$refs.ta
      if (!ta) return
      var w = this.wordAt()
      if (!w) return
      if (!force && w.text.length < 2) return
      var items = this.buildItems(w.text)
      if (!items.length) { this.closeTip(); return }
      var pos = this.measureTipPos()
      this.tip = { open: true, items: items, idx: 0, x: pos.x, y: pos.y }
    },
    refreshItems: function () {
      var w = this.wordAt()
      if (!w) { this.closeTip(); return }
      var items = this.buildItems(w.text)
      if (!items.length) { this.closeTip(); return }
      this.tip.items = items
      if (this.tip.idx >= items.length) this.tip.idx = 0
    },
    closeTip: function () { if (this.tip.open) this.tip.open = false },
    /** 按光标行列 + 字号推算浮层位置（等宽字体，误差在一个字符内） */
    measureTipPos: function () {
      var ta = this.$refs.ta
      var body = this.$refs.body
      var upto = ta.value.slice(0, ta.selectionStart)
      var lines = upto.split('\n')
      var col = lines[lines.length - 1].length
      var row = lines.length - 1
      var x = GUTTER_W + PAD + col * this.charW - ta.scrollLeft
      var y = PAD + (row + 1) * this.lineH - ta.scrollTop
      var maxW = body ? body.clientWidth - 240 : 600
      var maxH = body ? body.clientHeight : 300
      if (x > maxW) x = Math.max(GUTTER_W, maxW)
      if (x < GUTTER_W) x = GUTTER_W
      if (y > maxH - 60) y = Math.max(PAD, y - this.lineH - 190) // 下方空间不够时翻到上方
      return { x: x, y: y }
    },
    /** 采纳候选项：替换当前单词 */
    acceptTip: function (c) {
      var ta = this.$refs.ta
      var w = this.wordAt()
      if (!ta || !w) return
      var before = ta.value.slice(0, w.start)
      var after = ta.value.slice(w.end)
      // 关键字/函数后补一个空格，参数与表名不补
      var add = (c.kind === '关键字' || c.kind === '函数') ? ' ' : ''
      var v = before + c.insert + add + after
      this.$emit('update:modelValue', v)
      this.lineCount = v.split('\n').length
      this.closeTip()
      this.$nextTick(function () {
        var caret = before.length + c.insert.length + add.length
        ta.focus()
        ta.setSelectionRange(caret, caret)
      })
    },
    onKeyDown: function (e) {
      var ta = this.$refs.ta
      // Ctrl+空格：强制提示；Ctrl+回车：格式化
      if (e.ctrlKey && (e.key === ' ' || e.code === 'Space')) {
        e.preventDefault()
        this.openTip(true)
        return
      }
      if (e.ctrlKey && e.key === 'Enter') {
        e.preventDefault()
        this.doFormat()
        return
      }
      if (this.tip.open) {
        if (e.key === 'ArrowDown') { e.preventDefault(); this.tip.idx = (this.tip.idx + 1) % this.tip.items.length; return }
        if (e.key === 'ArrowUp') { e.preventDefault(); this.tip.idx = (this.tip.idx - 1 + this.tip.items.length) % this.tip.items.length; return }
        if (e.key === 'Enter' || e.key === 'Tab') {
          e.preventDefault()
          this.acceptTip(this.tip.items[this.tip.idx])
          return
        }
        if (e.key === 'Escape') { e.preventDefault(); this.closeTip(); return }
      }
      // Tab 缩进两个空格，避免焦点跳出编辑器
      if (e.key === 'Tab') {
        e.preventDefault()
        var v = ta.value
        var pos = ta.selectionStart
        var nv = v.slice(0, pos) + '  ' + v.slice(ta.selectionEnd)
        this.$emit('update:modelValue', nv)
        this.$nextTick(function () { ta.setSelectionRange(pos + 2, pos + 2) })
      }
    },
    /** 格式化当前内容（保留光标大致位置） */
    doFormat: function () {
      var ta = this.$refs.ta
      var old = this.modelValue || ''
      var out = formatSQL(old)
      if (!ta) { this.$emit('update:modelValue', out); return }
      var caret = Math.min(ta.selectionStart || 0, out.length)
      this.$emit('update:modelValue', out)
      this.lineCount = out.split('\n').length
      this.$nextTick(function () {
        ta.focus()
        ta.setSelectionRange(caret, caret)
      })
    },
    /** 在光标处插入文本（供父组件「插入条件参数」使用） */
    insertText: function (text) {
      var ta = this.$refs.ta
      if (!ta) return
      var v = ta.value
      var s = ta.selectionStart == null ? v.length : ta.selectionStart
      var e2 = ta.selectionEnd == null ? v.length : ta.selectionEnd
      var nv = v.slice(0, s) + text + v.slice(e2)
      this.$emit('update:modelValue', nv)
      this.lineCount = nv.split('\n').length
      this.$nextTick(function () {
        ta.focus()
        ta.setSelectionRange(s + text.length, s + text.length)
      })
    },
    focus: function () { if (this.$refs.ta) this.$refs.ta.focus() }
  }
}
</script>
