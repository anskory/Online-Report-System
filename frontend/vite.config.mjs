/* ============================================================
 * Vite 配置（单入口 SPA + 独立公开页）
 * 设计要点：
 *  1) base 为 '/'：产物以 /assets/xxx 绝对路径引用，地址为干净 URL
 *     （/login、/reports、/view?id=1），不带前缀也不带 .html。
 *  2) 入口两个：
 *     - index.html  → SPA 宿主，装载 src/main.js（路由 + 布局框架 + 所有业务页）；
 *     - public.html → 公开分享页，独立单页（Go 端 /r/:publicId 直出，无需登录与框架）。
 *  3) 产物输出到 backend/internal/webui/dist，由 go:embed 打进单二进制。
 *  4) 页面为 .vue 单文件组件（模板构建期编译），使用 Vue 运行时构建。
 *  5) 开发期由 rs-spa-fallback 插件把无扩展名路径重写到 /index.html，
 *     与 Go 端 Serve 的回落规则保持一致。
 * ============================================================ */
import { fileURLToPath, URL } from 'node:url'
import { resolve } from 'node:path'
import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'

const root = fileURLToPath(new URL('.', import.meta.url))

/** 多入口：index 为 SPA 宿主，public 为公开分享页（新增独立页时在此登记） */
const PAGES = ['index', 'public']

/**
 * 开发期 SPA 回落：/login、/reports?id=1 → /index.html（前端路由接管）。
 * 带扩展名的请求（.js/.css/.png…）与接口、HMR、源码模块一律放行，
 * 否则会出现「JS 被兜成 HTML」这类难查的白屏。
 */
function spaFallback() {
  const RESERVED = ['/api', '/r/', '/@', '/src/', '/node_modules/', '/assets/', '/vite', '/.well-known']
  return {
    name: 'rs-spa-fallback',
    configureServer(server) {
      server.middlewares.use((req, _res, next) => {
        const url = req.url || '/'
        const qi = url.indexOf('?')
        const pathname = qi < 0 ? url : url.slice(0, qi)
        const search = qi < 0 ? '' : url.slice(qi)
        if (RESERVED.some((p) => pathname.startsWith(p))) return next()
        if (/\.[a-z0-9]+$/i.test(pathname)) return next()
        req.url = '/index.html' + search
        next()
      })
    },
  }
}

export default defineConfig(({ mode }) => {
  // 只读取带 VITE_ 前缀的变量，避免把服务端密钥带进前端
  const env = loadEnv(mode, root, 'VITE_')
  // 开发期接口代理目标（默认与 config.yaml 的 server.port 一致）
  const backendOrigin = env.VITE_BACKEND_ORIGIN || 'http://127.0.0.1:18080'

  return {
    base: '/',
    plugins: [vue(), spaFallback()],
    resolve: {
      alias: [{ find: '@', replacement: resolve(root, 'src') }],
    },
    define: {
      __VUE_OPTIONS_API__: 'true',
      __VUE_PROD_DEVTOOLS__: 'false',
      __VUE_PROD_HYDRATION_MISMATCH_DETAILS__: 'false',
    },
    server: {
      // 访问地址形如 http://127.0.0.1:5173/login
      port: 5173,
      host: '127.0.0.1',
      open: '/login',
      proxy: {
        // 登录态、查询、导出等接口全部转发到 Go 服务，前端无需处理 CORS
        '/api': { target: backendOrigin, changeOrigin: true },
        // 公开分享链接 /r/:publicId 由 Go 端返回页面，dev 期一并转发便于联调
        '/r': { target: backendOrigin, changeOrigin: true },
      },
    },
    build: {
      outDir: resolve(root, '../backend/internal/webui/dist'),
      emptyOutDir: true,
      assetsDir: 'assets',
      sourcemap: false,
      chunkSizeWarningLimit: 2000,
      rollupOptions: {
        input: Object.fromEntries(PAGES.map((name) => [name, resolve(root, `${name}.html`)])),
        output: {
          // 第三方库单独成块，页面代码变更不影响其缓存
          manualChunks(id) {
            if (!id.includes('node_modules')) return
            if (id.includes('element-plus')) return 'element-plus'
            if (id.includes('/vue/') || id.includes('@vue/')) return 'vue'
            return 'vendor'
          },
        },
      },
    },
  }
})
