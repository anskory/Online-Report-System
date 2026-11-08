/* ============================================================
 * 应用引导：安装 Element Plus（中文）、全量图标、全局属性与路由
 * 入口统一调用 createRsApp(App, { router }).mount('#app')
 * ============================================================ */
import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import ElementPlusLocaleZhCn from 'element-plus/es/locale/lang/zh-cn'
import * as ElementPlusIcons from '@element-plus/icons-vue'

import 'element-plus/dist/index.css'
// Element Plus 暗色模式变量：由 html 根节点上的 dark class 驱动（见 lib/theme.js）
import 'element-plus/theme-chalk/dark/css-vars.css'
import '../styles/style.css'

import { RS, setRouter } from './rs.js'
import { initTheme } from './theme.js'

// 挂载前先应用主题，避免亮/暗闪屏（index.html 里另有更早的内联脚本兜底）
initTheme()

/** 注册全量图标为 i-xxx 全局组件（模板里写 <i-user> 即可用） */
function installIcons(app) {
  for (const name in ElementPlusIcons) {
    app.component('i-' + name.replace(/([a-z0-9])([A-Z])/g, '$1-$2').toLowerCase(), ElementPlusIcons[name])
  }
}

/**
 * 创建应用实例
 * @param {import('vue').Component} root 根组件（App.vue，或 public 这种独立页组件）
 * @param {{router?: import('vue-router').Router}} [options] 传入 router 时同时装载路由
 * @returns {import('vue').App} Vue 应用实例，调用方 .mount('#app')
 */
export function createRsApp(root, options = {}) {
  const app = createApp(root)
  app.use(ElementPlus, { locale: ElementPlusLocaleZhCn })
  installIcons(app)

  if (options.router) {
    app.use(options.router)
    // 让 rs.js 内的跳转（退出登录、401、权限守卫）走前端路由，避免整页刷新
    setRouter(options.router)
  }

  // 模板里可直接用 RS.xxx（退出登录等）与 can('perm') 控制按钮显隐
  app.config.globalProperties.RS = RS
  app.config.globalProperties.can = (perm) => RS.can(perm)
  return app
}

export default createRsApp
