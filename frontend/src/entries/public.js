/* public 页入口：挂载 Public.vue 到 #app */
import { createRsApp } from '@/lib/bootstrap.js'
import Page from '@/pages/Public.vue'

createRsApp(Page).mount('#app')