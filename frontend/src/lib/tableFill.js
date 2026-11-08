/**
 * el-table 自适应高度 mixin
 *
 * 目标：表格高度 = 内容区剩余高度，数据在表格内部滚动（表头固定、分页固定），
 * 而不是让表格无限长、整页跟着往下滚。
 *
 * 使用方式（三步）：
 *   1) 页面根节点加 class="rs-page-fill"，内容容器 class="page-body"（样式见 styles/style.css）
 *   2) 表格外层包裹 <div class="table-wrap" ref="tableWrap">
 *   3) el-table 绑定 :height="tableHeight"，并在组件里混入本 mixin
 *
 * 高度由 ResizeObserver 监听包裹层实时取值，因此侧栏折叠、搜索区展开/收起、
 * 窗口缩放、分页器出现/消失都能自动跟随，无需手动算 calc()。
 */
const tableFill = {
  data() {
    return { tableHeight: 0 }
  },
  mounted() {
    // 首帧 DOM 测量放到 nextTick：此时布局已完成，clientHeight 才是最终值
    this.$nextTick(this.measureTable)
    if (typeof ResizeObserver !== 'undefined') {
      this._ro = new ResizeObserver(() => this.measureTable())
      const el = this.$refs.tableWrap
      if (el) this._ro.observe(el)
    } else {
      // 兜底：老浏览器只监听窗口尺寸变化
      window.addEventListener('resize', this.measureTable)
    }
  },
  beforeUnmount() {
    if (this._ro) {
      this._ro.disconnect()
      this._ro = null
    }
    window.removeEventListener('resize', this.measureTable)
  },
  methods: {
    /** 按包裹层可视高度设置表格高度；1px 以内抖动忽略，避免与浏览器取整形成死循环 */
    measureTable() {
      const el = this.$refs.tableWrap
      if (!el) return
      const h = Math.max(120, Math.floor(el.clientHeight))
      if (Math.abs(h - this.tableHeight) > 1) this.tableHeight = h
    },
  },
}

export default tableFill
