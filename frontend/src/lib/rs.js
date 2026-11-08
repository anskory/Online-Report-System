/* ============================================================
 * 在线报表系统 - 前端公共库（纯逻辑层，不含 UI 装配）
 * 提供：令牌管理、API 封装（JWT 携带 / 401 跳转）、权限守卫、通用工具
 *
 * 说明：
 *  - 应用实例的创建与 Element Plus/图标的安装见 src/lib/bootstrap.js；
 *  - 页面组件通过 `import { RS } from '@/lib/rs.js'` 使用，模板内由
 *    globalProperties 暴露（见 bootstrap.js），可直接写 RS.xxx 与 can('perm')。
 * ============================================================ */
import { reactive } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

const TOKEN_KEY = 'rs_token'
const USER_KEY = 'rs_user'

/**
 * 前端路由实例，由 bootstrap.js 在安装路由后注入。
 * 注入后所有页面跳转走 router（不刷整页）；独立页（如公开分享页）没有路由时
 * 自动退化为 location 跳转，行为保持一致。
 */
let routerRef = null
export function setRouter(router) {
  routerRef = router
}

/**
 * 登录态聚合信息（响应式）：角色、权限点、导航菜单。
 * 由 /api/auth/me 一次性填充，模板里直接用 RS.me.xxx 或 can('perm') 即可，
 * 数据回来后相关节点会自动重渲染。
 */
const me = reactive({ loaded: false, user: {}, roles: [], perms: [], menus: [] })
let mePromise = null

/**
 * 归一化菜单路径，兼容历史数据与配置写法差异。
 *
 * 服务端菜单表（sys_menu.path）在早期版本里存的是免构建模式的地址，
 * 例如 `/ui/reports.html`；前端早已改成干净 URL（`/reports`），
 * 若直接把库里原值丢给 vue-router，会匹配不到任何路由而落到兜底重定向，
 * 表现就是「点菜单没反应」。这里统一在这一层收敛：
 *   /ui/reports.html → /reports
 *   reports.html     → /reports
 *   /view?id=1       → /view?id=1（保留查询串）
 *
 * @param {string} raw 菜单里的 path 原值
 * @returns {string} 以 / 开头的站内路径；外链原样返回；空值返回 ''
 */
export function normalizeMenuPath(raw) {
  if (!raw) return ''
  const s = String(raw).trim()
  if (!s) return ''
  // 外链与协议相对地址不做改写（菜单里允许配外部报表系统）
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(s) || s.slice(0, 2) === '//') return s

  const cut = s.search(/[?#]/)
  let p = cut < 0 ? s : s.slice(0, cut)
  const tail = cut < 0 ? '' : s.slice(cut)

  p = p.replace(/^\/ui(?=\/|$)/, '') // 去掉历史 /ui 前缀
  p = p.replace(/\.html?$/i, '') // 去掉页面后缀
  if (p.charAt(0) !== '/') p = '/' + p
  p = p.replace(/\/{2,}/g, '/')
  if (p.length > 1 && p.charAt(p.length - 1) === '/') p = p.slice(0, -1)
  return (p || '/') + tail
}

/**
 * 读取响应体并解析 JSON。
 * 后端未启动 / 开发代理失效时，Vite 会返回 500 空响应，直接 res.json() 只会抛
 * 「Failed to execute 'json' on 'Response'」这类无法定位的报错；这里统一转成可读文案。
 */
async function readJSON(res) {
  const text = await res.text()
  if (!text) {
    throw new Error('接口无响应（HTTP ' + res.status + '）：后端可能未启动（默认 18080），或开发代理目标配置有误')
  }
  try {
    return JSON.parse(text)
  } catch (e) {
    throw new Error('接口返回非 JSON（HTTP ' + res.status + '）：' + text.slice(0, 120))
  }
}

/** 网络层失败（连接被拒、断网）统一转成中文提示，保留原始线索便于排查 */
function withNetworkHint(promise, path) {
  return promise.catch((e) => {
    const raw = e && e.message ? e.message : '网络异常'
    throw new Error('无法连接后端接口 ' + path + '（' + raw + '），请确认 Go 服务已启动')
  })
}

export const RS = {
  me,
  /** 菜单路径归一化（模板里可写 RS.menuPath(m.path)） */
  menuPath: normalizeMenuPath,
  /** 当前登录令牌 */
  token() {
    return localStorage.getItem(TOKEN_KEY) || ''
  },
  setToken(t, user) {
    localStorage.setItem(TOKEN_KEY, t)
    if (user) localStorage.setItem(USER_KEY, JSON.stringify(user))
  },
  /** 当前登录用户 {id, username, role} */
  user() {
    try {
      return JSON.parse(localStorage.getItem(USER_KEY) || '{}')
    } catch (e) {
      return {}
    }
  },
  /** 是否超级管理员（以 /me 返回的角色为准，未加载时回退本地缓存的 role） */
  isAdmin() {
    if (me.loaded) return me.roles.some((r) => r.roleKey === 'admin')
    return RS.user().role === 'admin'
  },
  /**
   * 站内跳转：有路由时用 router.replace（不产生历史堆积、不刷新整页），
   * 否则退回 location.href。供退出登录、401、权限守卫与页面间跳转统一使用。
   */
  navTo(target) {
    if (!routerRef) {
      location.href = target
      return
    }
    const cur = routerRef.currentRoute.value.fullPath
    if (cur === target) return
    routerRef.replace(target).catch(() => {})
  },
  logout() {
    localStorage.removeItem(TOKEN_KEY)
    localStorage.removeItem(USER_KEY)
    me.loaded = false
    me.user = {}
    me.roles = []
    me.perms = []
    me.menus = []
    mePromise = null
    RS.navTo('/login')
  },
  /** 未登录则跳转登录页（公开页勿调用） */
  requireLogin() {
    if (!RS.token()) {
      RS.navTo('/login?back=' + encodeURIComponent(location.pathname + location.search))
      return false
    }
    return true
  },

  /** 拉取当前用户的角色/权限点/导航菜单（并发去重，失败可重试） */
  loadMe() {
    if (mePromise) return mePromise
    mePromise = RS.api('GET', '/api/auth/me')
      .then((d) => {
        me.user = d.user || {}
        me.roles = d.roles || []
        me.perms = d.perms || []
        me.menus = d.menus || []
        me.loaded = true
        if (me.user.id) RS.setToken(RS.token(), me.user)
        return me
      })
      .catch((e) => {
        mePromise = null
        throw e
      })
    return mePromise
  },

  /** 页面初始化：确保登录态与权限信息就绪 */
  ready() {
    if (!RS.requireLogin()) return Promise.reject(new Error('未登录'))
    if (me.loaded) return Promise.resolve(me)
    return RS.loadMe()
  },

  /** 强制重载角色/权限/菜单（角色或菜单变更后调用，导航立即刷新） */
  refreshMe() {
    mePromise = null
    me.loaded = false
    return RS.loadMe()
  },

  /** 是否拥有某权限点（超级管理员的 perms 为 ['*']） */
  can(perm) {
    if (!perm) return true
    if (me.perms.indexOf('*') >= 0) return true
    return me.perms.indexOf(perm) >= 0
  },

  /** 页面级权限守卫：无权限时提示并跳到第一个有权限的页面 */
  guard(perm) {
    return RS.ready().then(() => {
      if (perm && !RS.can(perm)) {
        ElMessage.error('没有访问该页面的权限')
        const target = RS.homePath()
        if (target !== location.pathname) setTimeout(() => RS.navTo(target), 900)
        return Promise.reject(new Error('forbidden'))
      }
      return me
    })
  },

  /** 落地页：第一个可见菜单的页面（都没权限时回报表首页） */
  homePath() {
    const list = me.menus || []
    for (let i = 0; i < list.length; i++) {
      const p = normalizeMenuPath(list[i].path)
      if (p) return p
      const kids = list[i].children || []
      for (let j = 0; j < kids.length; j++) {
        const cp = normalizeMenuPath(kids[j].path)
        if (cp) return cp
      }
    }
    return '/reports'
  },

  /**
   * API 请求封装
   * @param {string} method GET/POST/PUT/DELETE
   * @param {string} path   以 /api 开头
   * @param {object} [body] 请求体（自动 JSON 序列化）
   * @returns {Promise<any>} 成功时 resolve data 字段
   */
  api(method, path, body) {
    const opts = { method, headers: { 'Content-Type': 'application/json' } }
    const tk = RS.token()
    if (tk) opts.headers.Authorization = 'Bearer ' + tk
    if (body !== undefined) opts.body = JSON.stringify(body)
    return withNetworkHint(fetch(path, opts), path)
      .then((res) => {
        if (res.status === 401) {
          localStorage.removeItem(TOKEN_KEY)
          localStorage.removeItem(USER_KEY)
          // 同步清空登录态缓存：否则 me.loaded/mePromise 仍是旧用户数据，
          // 重新登录后 loadMe() 直接返回缓存，会看到上一个账号的菜单与权限
          me.loaded = false
          me.user = {}
          me.roles = []
          me.perms = []
          me.menus = []
          mePromise = null
          RS.navTo('/login?back=' + encodeURIComponent(location.pathname + location.search))
          throw new Error('未登录或登录已过期')
        }
        return readJSON(res)
      })
      .then((j) => {
        if (j.code !== 0) throw new Error(j.msg || '请求失败(' + j.code + ')')
        return j.data
      })
  },
  get(path) {
    return RS.api('GET', path)
  },
  post(path, body) {
    return RS.api('POST', path, body)
  },
  put(path, body) {
    return RS.api('PUT', path, body)
  },
  del(path) {
    return RS.api('DELETE', path)
  },

  /** 公开访问 API（匿名，令牌放请求头） */
  pubApi(publicId, token, method, path, body) {
    const opts = { method, headers: { 'Content-Type': 'application/json', 'X-Report-Token': token } }
    if (body !== undefined) opts.body = JSON.stringify(body)
    return withNetworkHint(fetch('/api/pub/' + publicId + path, opts), path)
      .then(readJSON)
      .then((j) => {
        if (j.code !== 0) throw new Error(j.msg || '请求失败(' + j.code + ')')
        return j.data
      })
  },

  /** 查询串参数 */
  qs(name) {
    return new URLSearchParams(location.search).get(name) || ''
  },

  /** HTML 转义（弹窗里展示 SQL/报错原文时防止破坏结构） */
  esc(s) {
    return String(s === undefined || s === null ? '' : s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;')
  },

  /**
   * 通用错误提示。
   * 查询类报错现在会带上「绑定参数 + SQL 摘要」，往往超过一两百字，
   * 轻提示看不下也复制不了，故长消息改用可滚动的弹窗，便于截图/复制反馈。
   */
  alertErr(e) {
    const msg = e && e.message ? e.message : String(e)
    if (msg.length > 120) {
      const ret = ElMessageBox.alert(
        '<pre style="white-space:pre-wrap;word-break:break-all;margin:0;max-height:50vh;overflow:auto">' +
          RS.esc(msg) +
          '</pre>',
        '操作失败',
        { confirmButtonText: '知道了', dangerouslyUseHTMLString: true }
      )
      if (ret && ret.catch) ret.catch(() => {}) // 关闭按钮触发的 reject 不向外抛
      return
    }
    ElMessage.error(msg)
  },
}

export default RS
