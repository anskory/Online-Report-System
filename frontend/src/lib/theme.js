/* ============================================================
 * theme.js - 明暗主题切换
 * 设计令牌在 styles/style.css 的 :root / html.dark 中定义；
 * Element Plus 暗色变量由 bootstrap.js 引入的 dark/css-vars.css 提供，
 * 两者统一由 html 根节点上的 dark class 驱动。
 * ============================================================ */

/** localStorage 持久化键：与 index.html 里的防闪烁内联脚本保持一致 */
const THEME_KEY = 'rs_theme'

/** 读取当前主题（'light' | 'dark'，异常时回退 light） */
export function currentTheme() {
  try {
    return localStorage.getItem(THEME_KEY) === 'dark' ? 'dark' : 'light'
  } catch (e) {
    return 'light'
  }
}

/** 应用主题到 html 根节点（Element Plus 用 dark class，自定义变量用 data-theme） */
export function applyTheme(mode) {
  const root = document.documentElement
  const dark = mode === 'dark'
  root.classList.toggle('dark', dark)
  root.dataset.theme = dark ? 'dark' : 'light'
}

/** 保存并应用主题 */
export function setTheme(mode) {
  try {
    localStorage.setItem(THEME_KEY, mode)
  } catch (e) { /* 隐私模式下写不进去也无妨，仅本次会话生效 */ }
  applyTheme(mode)
}

/** 切换主题并返回新主题（'light' | 'dark'） */
export function toggleTheme() {
  const next = currentTheme() === 'dark' ? 'light' : 'dark'
  setTheme(next)
  return next
}

/** 应用启动时调用：按用户上次选择初始化（SPA 内多入口共用） */
export function initTheme() {
  applyTheme(currentTheme())
}
