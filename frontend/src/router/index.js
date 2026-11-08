/* ============================================================
 * 路由表：一个页面一条路由，meta 里带标题与权限点。
 *
 * 约定：
 *  - 需要框架（左侧菜单 + 顶栏）的页面挂在 AppLayout 的 children 下；
 *  - 登录页、公开分享页不带框架，各自独立；
 *  - meta.perm 只写「静态权限点」，像 /config 这种「新建用 create、编辑用
 *    update」的动态权限由页面内部的 RS.guard 判断，避免这里误拦；
 *  - meta.activeMenu 用于「不在菜单里的页面」也能点亮对应菜单（如 /view）。
 * ============================================================ */
import { createRouter, createWebHistory } from 'vue-router'
import { RS } from '@/lib/rs.js'

const APP_NAME = '在线报表系统'

const routes = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/pages/Login.vue'),
    meta: { public: true, title: '登录' },
  },
  {
    path: '/',
    component: () => import('@/layout/AppLayout.vue'),
    redirect: '/reports',
    children: [
      {
        path: 'reports',
        name: 'reports',
        component: () => import('@/pages/Reports.vue'),
        meta: { title: '报表中心', perm: 'report:view' },
      },
      {
        path: 'view',
        name: 'view',
        component: () => import('@/pages/View.vue'),
        // 报表查看由列表带 id 进入，本身不是菜单项，点亮「报表中心」
        meta: { title: '报表查看', perm: 'report:view', activeMenu: '/reports' },
      },
      {
        path: 'config',
        name: 'config',
        component: () => import('@/pages/Config.vue'),
        // 新建/编辑的权限点不同，交由页面内 RS.guard 动态判断；
        // 入口已收敛到「报表中心」按钮，这里点亮报表中心菜单
        meta: { title: '报表配置', activeMenu: '/reports' },
      },
      {
        path: 'datasources',
        name: 'datasources',
        component: () => import('@/pages/Datasources.vue'),
        meta: { title: '数据源管理', perm: 'datasource:list' },
      },
      {
        path: 'dictionaries',
        name: 'dictionaries',
        component: () => import('@/pages/Dictionaries.vue'),
        meta: { title: '字典管理', perm: 'dict:list' },
      },
      {
        path: 'users',
        name: 'users',
        component: () => import('@/pages/Users.vue'),
        meta: { title: '用户管理', perm: 'system:user:list' },
      },
      {
        path: 'roles',
        name: 'roles',
        component: () => import('@/pages/Roles.vue'),
        meta: { title: '角色管理', perm: 'system:role:list' },
      },
      {
        path: 'menus',
        name: 'menus',
        component: () => import('@/pages/Menus.vue'),
        meta: { title: '菜单管理', perm: 'system:menu:list' },
      },
    ],
  },
  // 兜底：未知路径回到报表中心（未登录时再被守卫送登录页）
  { path: '/:pathMatch(.*)*', redirect: '/reports' },
]

const router = createRouter({
  history: createWebHistory('/'),
  routes,
  // 切页回到顶部，避免长表格页沿用上一个页面的滚动位置
  scrollBehavior: () => ({ top: 0 }),
})

/**
 * 全局前置守卫：只做「登录态」这一件事。
 * 权限点校验留在各页面（例如 /config 依赖 id 判断 create/update），
 * 两边都做会互相覆盖跳转目标，排查困难。
 */
router.beforeEach(async (to) => {
  if (to.meta && to.meta.public) return true

  if (!RS.token()) {
    return { path: '/login', query: { back: to.fullPath } }
  }
  try {
    await RS.loadMe()
  } catch (e) {
    // 令牌过期或接口异常：回登录页并带上原地址
    return { path: '/login', query: { back: to.fullPath } }
  }

  const perm = to.meta && to.meta.perm
  if (perm && !RS.can(perm)) {
    const home = RS.homePath()
    return home === to.path ? false : home
  }
  return true
})

router.afterEach((to) => {
  const title = (to.meta && to.meta.title) || ''
  document.title = title ? title + ' - ' + APP_NAME : APP_NAME
})

export default router
