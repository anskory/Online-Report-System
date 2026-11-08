// Package webui 前端构建产物（Vue 3 + Element Plus + Vite 单入口 SPA）。
//
// 前端源码位于 report-system/frontend，构建（npm run build）输出到
// internal/webui/dist，再经 go:embed 打进单一二进制。产物只有两个页面：
//
//	index.html    SPA 宿主页，承载 login/reports/config… 所有前端路由
//	public.html   公开分享页，由 /r/:publicId 直出
//	assets/*      带内容哈希的 js/css
//
// 运行期按「干净 URL」对外服务：
//
//	/login        → index.html（前端路由渲染登录页）
//	/reports      → index.html（前端路由渲染报表中心）
//	/assets/x.js  → assets/x.js
//
// 注意 go:embed 在编译期取快照：改了前端必须重新 npm run build + go build，
// 否则二进制里仍是旧界面（启动日志会用 Info() 如实打印嵌入内容）。
package webui

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"sort"
	"strings"
)

//go:embed all:dist
var distFiles embed.FS

// distFS 去掉 dist/ 前缀后的产物文件系统。
// 在 init() 里装配：包级变量先于 init() 执行，若写成
// `var distFS = mustSub(distFiles)` 之类尚可，但依赖注入顺序容易出错，统一放 init() 更直观。
var distFS fs.FS

func init() {
	sub, err := fs.Sub(distFiles, "dist")
	if err != nil {
		// all:dist 是编译期固定的嵌入目录，Sub 只会在路径非法时失败，属于编程错误
		panic("webui: 初始化嵌入文件系统失败: " + err.Error())
	}
	distFS = sub
}

// buildHint 前端产物缺失时的兜底页面，避免运维只看到 404
const buildHint = `<html lang="zh-CN"><head><meta charset="utf-8"><title>前端资源未构建</title></head>
<body style="font-family:system-ui,'Microsoft YaHei';padding:40px;line-height:1.9;color:#303133">
<h3 style="color:#c45656">前端资源未构建</h3>
<p>请先在 <code>report-system/frontend</code> 目录执行：</p>
<pre style="background:#f5f7fa;padding:12px;border-radius:6px">npm install
npm run build</pre>
<p>产物输出到 <code>backend/internal/webui/dist</code>，然后重新编译并重启后端。</p>
</body></html>`

// BuildHint 返回「前端未构建」提示页，供服务端在产物缺失时兜底（避免只看到 404）
func BuildHint() []byte {
	return []byte(buildHint)
}

// FS 返回构建产物文件系统（根为产物目录），供测试或额外挂载使用
func FS() http.FileSystem {
	return http.FS(distFS)
}

// PublicHTML 返回公开访问页内容（服务 /r/:publicId）
//
// 用包级的 fs.ReadFile 而不是 distFS.ReadFile：distFS 的类型是 fs.FS，
// 接口上只有 Open，ReadFile 是 fs / embed.FS 的额外方法。
func PublicHTML() []byte {
	b, err := fs.ReadFile(distFS, "public.html")
	if err != nil {
		return []byte(buildHint)
	}
	return b
}

// Built 报告前端构建产物是否存在（以 SPA 宿主页为标志）
func Built() bool {
	_, err := fs.Stat(distFS, "index.html")
	return err == nil
}

// Info 汇报「嵌入到二进制里的」产物概况，供启动日志使用。
//
// 目的很实际：go:embed 的内容在编译期就固定了，dist 目录事后更新不会影响
// 已编好的 exe。启动时把嵌入内容如实打出来，就能直接区分
// 「前端改了但没重编」与「后端根本没起来」，不必再去猜。
func Info() string {
	entries, err := fs.ReadDir(distFS, ".")
	if err != nil {
		return "前端产物：读取失败（" + err.Error() + "）"
	}
	var pages []string
	assets := 0
	var indexSize int64
	for _, e := range entries {
		if e.IsDir() {
			if e.Name() == "assets" {
				if sub, err := fs.ReadDir(distFS, "assets"); err == nil {
					assets = len(sub)
				}
			}
			continue
		}
		if strings.HasSuffix(e.Name(), ".html") {
			pages = append(pages, e.Name())
			if e.Name() == "index.html" {
				if fi, err := e.Info(); err == nil {
					indexSize = fi.Size()
				}
			}
		}
	}
	if len(pages) == 0 {
		return "前端产物：未嵌入（二进制内 dist 为空）"
	}
	sort.Strings(pages)
	return fmt.Sprintf("前端产物：已嵌入 %s（index.html %d 字节）、assets %d 个文件",
		strings.Join(pages, " + "), indexSize, assets)
}

// Serve 输出前端资源，命中返回 true。规则：
//   - 带扩展名（/assets/index-xxx.js、/favicon.ico）→ 当静态资源取，取不到返回 false，
//     绝不回落到 HTML，否则浏览器会把 HTML 当 JS 执行，报错极难定位；
//   - 无扩展名（/login、/reports、/view?id=1）→ 一律输出 SPA 宿主页 index.html，
//     具体渲染哪个页面由前端路由（frontend/src/router）决定。
func Serve(w http.ResponseWriter, r *http.Request) bool {
	p := path.Clean(r.URL.Path)
	if path.Ext(p) != "" {
		return serveAsset(w, r, p)
	}
	return serveApp(w, r)
}

// serveAsset 输出 dist 下真实存在的静态文件
func serveAsset(w http.ResponseWriter, r *http.Request, p string) bool {
	name := strings.TrimPrefix(p, "/")
	if name == "" {
		return false
	}
	if _, err := fs.Stat(distFS, name); err != nil {
		return false
	}
	serveFile(w, r, name)
	return true
}

// serveApp 输出 SPA 宿主页（产物缺失时返回 false，由调用方给出构建提示）
func serveApp(w http.ResponseWriter, r *http.Request) bool {
	if _, err := fs.Stat(distFS, "index.html"); err != nil {
		return false
	}
	serveFile(w, r, "index.html")
	return true
}

// serveFile 输出产物文件。
//
// 不能用 http.FileServer：它对以 "/index.html" 结尾的路径会 301 到 "./" 做目录
// 规范化（net/http 的 serveFile 里 strings.HasSuffix(path, "/index.html") 分支），
// 而 SPA 宿主页的文件名恰好是 index.html，于是
//   /login → 输出 index.html → 301 "./" → / → 302 /login → …
// 形成重定向死循环（浏览器报 ERR_TOO_MANY_REDIRECTS）。
//
// 这里直接用 http.ServeContent：按文件名后缀推断 Content-Type，并支持 Range / 304。
func serveFile(w http.ResponseWriter, r *http.Request, name string) {
	if name == "index.html" {
		// 宿主页不带哈希，禁用缓存保证重新发布后刷新即生效；
		// 其余资源文件名带内容哈希，可长期缓存，这里不加干预。
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	}
	f, err := distFS.Open(name)
	if err != nil {
		http.Error(w, "资源不存在："+name, http.StatusNotFound)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "资源不可读："+name, http.StatusInternalServerError)
		return
	}
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, name, st.ModTime(), rs)
		return
	}
	// 兜底：底层文件系统不支持 Seek 时整块读出（只用于小文件）
	b, err := fs.ReadFile(distFS, name)
	if err != nil {
		http.Error(w, "资源不可读："+name, http.StatusInternalServerError)
		return
	}
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}
