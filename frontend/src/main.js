/* ============================================================
 * 单入口：装配 Element Plus / 路由 / 全局属性，然后挂载根组件
 * 一个 HTML（index.html）承载所有业务页面，导航不刷整页。
 * ============================================================ */
import { createRsApp } from '@/lib/bootstrap.js'
import router from '@/router'
import App from '@/App.vue'

createRsApp(App, { router }).mount('#app')
