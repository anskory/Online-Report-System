# 在线报表系统 · Online Report System

> 基于配置的 Web 报表平台：在界面上配置**数据源 → SQL → 查询条件 → 展示列**，即可上线一张可查询、可排序、可导出 Excel 的报表，新增报表**无需改代码、无需重新发版**。
>
> 后端 Go（Gin + GORM），前端 Vue 3（Element Plus + Vite），前端产物 `go:embed` 打进二进制——**单文件交付**，默认 SQLite 内嵌，无需额外安装数据库。

当前版本：`v1.2`　|　默认端口：`18080`　|　默认账号：`admin / admin123`（首次登录后请立即修改）

---

## 一、核心特性

| 能力 | 说明 |
|------|------|
| 报表配置即上线 | 五步配置（基本信息 → 数据源与 SQL → 查询条件 → 展示列 → 发布），保存即生效 |
| 多数据源 | Oracle / MySQL / PostgreSQL / SQL Server / SQLite，可同时接入多个实例 |
| 元数据浏览 | 表清单、字段名与注释在线浏览，辅助编写 SQL |
| 配置化查询条件 | 文本框、数字、日期、日期范围、下拉；下拉选项支持**固定值 / 数据字典 / 自定义 SQL**；支持默认值、必填、多选、拖动排序、多行多值（逗号/换行批量搜索） |
| 数据字典 | 支持聚合式（类型 + 值同表）与分离式（类型表、值表分开），表名支持跨模式 `OWNER.TABLE`；查询条件可联动字典 |
| 结果表格 | 列宽 / 顺序 / 显隐可配置（按用户隔离保存），表头排序，服务端分页（Oracle 11g `ROWNUM` / 12c+ `OFFSET FETCH` 自动适配） |
| Excel 导出 | 小结果集同步流式导出；超过阈值自动转**异步任务**（可查进度、下载）；xlsx 超百万行自动拆 Sheet；也支持 CSV |
| 公开分享 | 报表可开启匿名访问链接 `/r/:publicId`，独立控制是否允许导出 |
| 权限体系 | JWT 登录 + 角色 + 菜单/按钮权限点 + 报表级授权；登录失败次数锁定（默认 5 次 / 10 分钟） |
| 审计与运维 | 操作审计日志（保留天数可配）、慢查询日志、健康检查 `/api/health` |
| 界面 | 科技蓝主题，支持**明暗模式切换**（随浏览器记忆），页面切换与侧栏折叠带过渡动画 |

---

## 二、技术栈

| 层次 | 选型 |
|------|------|
| 后端 | Go 1.22、Gin 1.10、GORM 1.30 |
| 系统库 | SQLite（纯 Go 实现，WAL 模式，默认）/ MySQL（可选） |
| 业务数据源 | `go-sql-driver/mysql`、`lib/pq`、`microsoft/go-mssqldb`、`sijms/go-ora/v2`（纯 Go Oracle）、`godror`（CGO Oracle，可选） |
| 认证 | `golang-jwt/jwt/v5`，密码 bcrypt，数据源密码 AES 加密存储 |
| 导出 | `xuri/excelize/v2`（StreamWriter 流式写） |
| 前端 | Vue 3.5 + Vue Router 4 + Element Plus 2.9 + Vite 6 |
| 打包 | 前端产物 → `backend/internal/webui/dist` → `go:embed` 单二进制 |

### Oracle 双驱动说明

| 场景 | 驱动 | 是否需要 Oracle 客户端 |
|------|------|------------------------|
| 默认构建（`build-all.ps1` / `build-and-run.bat`，`CGO_ENABLED=0`） | go-ora（纯 Go） | **不需要**，任意平台可用 |
| Windows + MinGW-w64 + Instant Client SDK，`-tags oracle` | godror（CGO） | 需要 `oci.dll` 等运行时 DLL（编译脚本会自动随包分发） |

> 纯 Go 驱动不支持 **TNS 描述符直连串**；遇到 TNS / RAC 场景请填「主机 + 端口 + 服务名」，或在 Windows 使用 godror 版本。

---

## 三、目录结构

```
report-system/
├── app/                        # 一键编译输出（可直接分发）
│   ├── windows/                #   report-server.exe + config.yaml + data/ + 启动服务.bat 等
│   ├── linux/                  #   report-server-amd64/arm64 + start.sh / stop.sh
│   └── macos/
├── backend/
│   ├── cmd/
│   │   ├── server/             # 服务入口：main.go（装配）、seed.go（初始角色/菜单/管理员）
│   │   └── hashgen/            # 小工具：生成 bcrypt 哈希（重置密码用）
│   ├── internal/
│   │   ├── api/                # Gin 路由、Handler、JWT 中间件、权限点校验、限流
│   │   ├── service/            # 业务层：auth / rbac / datasource / meta / report /
│   │   │                       #         query_engine / export / dict / column_config / audit
│   │   ├── datasource/         # 驱动注册制：oracle_cgo / oracle_purego / mysql / postgres / sqlserver / sqlite
│   │   ├── model/              # GORM 模型（用户、角色、菜单、报表、条件、列、字典、导出任务…）
│   │   ├── pkg/                # crypto（AES）、jwtutil、resp（统一响应）
│   │   ├── webui/              # go:embed dist + SPA 回落 + 未构建提示页
│   │   └── config/             # config.yaml 解析
│   ├── config.yaml             # 端口 / 密钥 / 导出 / 查询超时等
│   ├── deploy/                 # Dockerfile、docker-compose.yml、部署说明
│   ├── build-and-run.bat       # 开发用：前端构建 → go build → 前台启动
│   ├── build.bat / build.sh    # 单平台构建
│   ├── setup-and-build.ps1     # 首次环境准备（下载便携 Go/Node 等）
│   └── setup-oracle-build.ps1  # 准备 Windows CGO + Instant Client 编译环境
├── frontend/                   # Vue 3 源码（Vite，产物输出到 backend/internal/webui/dist）
│   └── src/
│       ├── pages/              # Login / Reports / Config / View / Public / Datasources /
│       │                       # Dictionaries / Users / Roles / Menus
│       ├── layout/             # AppLayout（侧栏 + 顶栏）、SideNav
│       ├── components/         # SqlEditor（语法着色 + 补全 + 行号）
│       ├── lib/                # rs.js（请求/权限/跳转）、theme.js（明暗主题）、tableFill.js
│       └── styles/style.css     # 设计令牌（--rs-*）+ 明暗主题
├── docs/                       # 开发文档、设计文档、UI 设计预览
├── build-all.ps1               # 三平台交叉编译（前端打包 + 组装 app/）
├── 一键编译.bat                # 双击入口 → 调用 build-all.ps1
└── README.md
```

---

## 四、快速开始

### 环境要求

| 用途 | 要求 |
|------|------|
| 运行服务 | 无依赖（单二进制，SQLite 内嵌） |
| 编译后端 | Go 1.22+（可用 `backend/.tools/go` 便携版） |
| 编译前端 | Node.js 18+ 与 npm |
| 系统库改 MySQL | 可选，改 `config.yaml` 的 `sys_db` 即可 |

### 方式 A：一键编译分发包（推荐）

双击项目根目录 **`一键编译.bat`**，脚本会依次完成：

1. 前端 `vite build` → `backend/internal/webui/dist`
2. `go mod tidy`（走 goproxy.cn）
3. 交叉编译 5 个目标：`windows/amd64`、`linux/amd64`、`linux/arm64`、`darwin/amd64`、`darwin/arm64`
4. 组装 `app/{windows,linux,macos}`，附带 `config.yaml`、`data/`、启动脚本

```powershell
# 可选参数
powershell -ExecutionPolicy Bypass -File .\build-all.ps1 -SkipFrontend        # 复用已有前端产物
powershell -ExecutionPolicy Bypass -File .\build-all.ps1 -ForceConfig        # 覆盖 app 下已有配置
powershell -ExecutionPolicy Bypass -File .\build-all.ps1 -Targets windows,linux
```

启动：

- **Windows**：进入 `app/windows`，双击 `启动服务.bat`（前台）；或 `后台启动.bat` + `停止服务.bat`
- **Linux**：`./start.sh`（前台）/ `./start.sh -d`（后台，日志 `logs/server.log`）/ `./stop.sh`
- **macOS**：同 Linux；若被 Gatekeeper 拦截：`xattr -dr com.apple.quarantine . && chmod +x start.sh stop.sh report-server-*`

访问 `http://127.0.0.1:18080`，默认账号 `admin / admin123`。

### 方式 B：开发一键构建运行

```bat
cd backend
build-and-run.bat        :: 前端 vite build → CGO_ENABLED=0 go build → 前台启动
```

### 方式 C：前后端分离调试（改前端热更新）

```bash
# 终端 1：后端
cd backend
go run ./cmd/server            # 监听 :18080

# 终端 2：前端（Vite dev server）
cd frontend
npm install
npm run dev                    # http://127.0.0.1:5173/login
```

Vite 已配置代理：`/api` 与 `/r` 转发到 `http://127.0.0.1:18080`（可用 `VITE_BACKEND_ORIGIN` 覆盖），无跨域问题。

> ⚠️ **前端改动必须重新构建后端才会生效**：`go:embed` 是编译期快照。改了 `frontend/` 后要 `npm run build` 再 `go build`（`build-and-run.bat` 已包含两步），否则页面还是旧的。启动日志会打印嵌入产物的概况，可据此确认是否为新构建。

---

## 五、配置说明（`config.yaml`）

> 仓库只提交 `backend/config.yaml.example`（示例密钥），真实密钥不入库。首次运行先复制一份：
> `cp backend/config.yaml.example backend/config.yaml`（Windows 用 `copy`），再按需修改。

| 配置项 | 默认 | 说明 |
|--------|------|------|
| `server.port` | `18080` | 监听端口 |
| `sys_db.driver` | `sqlite` | 系统库类型：`sqlite` / `mysql` |
| `sys_db.dsn` | `./data/report_sys.db` | SQLite 文件路径或 MySQL DSN（自动追加 WAL / busy_timeout / foreign_keys） |
| `security.jwt_secret` | 示例值 | JWT 签名密钥，**生产必须更换**；环境变量 `JWT_SECRET` 可覆盖 |
| `security.aes_key` | 示例值 | 数据源密码加密密钥，**生产必须更换**；环境变量 `AES_KEY` 可覆盖 |
| `security.login_max_fail` | `5` | 连续登录失败锁定阈值 |
| `security.login_lock_minutes` | `10` | 锁定时长（分钟） |
| `log.level` | `info` | 日志级别 |
| `log.slow_query_ms` | `5000` | 慢查询阈值 |
| `log.audit_retention_days` | `90` | 审计日志保留天数（启动先清一次，之后每 6 小时；`<=0` 关闭清理） |
| `log.audit_sync` | `false` | `true`=审计日志同步写（排障用，会影响响应耗时） |
| `log.audit_queue_size` | `2048` | 审计写入队列长度；满队列丢弃日志并计数（`/api/health` 的 `auditDropped`） |
| `export.dir` | `./data/exports` | 导出文件目录 |
| `export.sync_row_limit` | `10000` | 行数 ≤ 此值同步流式导出，超过转异步任务 |
| `export.workers` | `2` | 异步导出 Worker 数 |
| `export.retention_days` | `7` | 导出文件保留天数（自动清理） |
| `export.xlsx_sheet_max_row` | `1000000` | 单 Sheet 行数上限，超出自动拆 Sheet2… |
| `query.count_timeout_sec` | `5` | `COUNT(*)` 单独超时，超时降级为「总数未知」 |
| `query.count_cache_sec` | `30` | 总数统计缓存秒数，翻页复用 |
| `meta.cache_ttl_sec` | `300` | 表/字段元数据缓存有效期（秒）；`0`=不缓存，每次实时扫描 |
| `meta.table_limit` | `3000` | 单次返回表数量上限（超出截断并回传真实总数，关键字搜索不受限） |

---

## 六、使用流程：配置一张报表

1. **数据源管理** —— 新增数据源（类型 / 主机 / 端口 / 库名或服务名 / 账号密码），点击「测试连接」
2. **报表中心 → 报表配置** —— 填写基本信息（名称、分组、备注）
3. **数据源与 SQL** —— 选数据源 → 浏览表与字段 → 写 SQL（支持 `:参数名` 占位符，编辑器带语法着色与补全）
4. **查询条件** —— 逐项配置：参数名、显示名、控件类型、选项来源（固定/字典/SQL）、默认值、是否必填、多选、排序
5. **展示列** —— 勾选结果集字段，配置列名、宽度、对齐、是否默认显示、是否可排序
6. **预览 → 发布** —— 发布后出现在报表中心，可查看/导出；需要外部分享时开启「公开访问」，得到 `/r/:publicId` 链接

> 小技巧：配置化 SQL 里中文显示名与参数名是**解耦**的——参数名需为合法标识符（如 `steelNo`），中文写在「显示名」里；结果集列名可以带中文，填写「列名(结果集)」即可。

---

## 七、权限模型

- **认证**：JWT（`Authorization: Bearer <token>`），登录失败 `login_max_fail` 次锁定 `login_lock_minutes` 分钟
- **角色**：内置 `admin`（超级管理员，隐式拥有全部权限）、`user`（普通用户，仅查看已授权且已发布的报表），可自建角色
- **权限点**：菜单与按钮级，按 `资源:动作` 命名，例如 `report:create`、`report:publish`、`datasource:test`、`dict:list`、`system:user:list`、`system:audit:list`（日志查看）、`system:audit:export`、`system:audit:delete`
- **报表授权**：角色 ↔ 报表 多对多（`rpt_role_report`），普通用户只看到授权范围内的报表
- **菜单**：数据库驱动，可在「菜单管理」中增删改，前端按 `/api/menus/my` 动态渲染侧栏

---

## 八、API 概览

| 分组 | 端点 |
|------|------|
| 健康检查 | `GET /api/health` |
| 认证 | `POST /api/auth/login`、`POST /api/auth/refresh`、`GET /api/auth/me` |
| 报表列表 | `GET /api/reports`、`GET /api/reports/:id`、`GET /api/report-groups` |
| 报表配置 | `POST/PUT /api/reports`、`DELETE /api/reports/:id`、`POST /api/reports/:id/publish`、`POST /api/reports/:id/preview`、`POST·PUT·DELETE /api/reports/:id/public` |
| 查询 | `POST /api/reports/:id/query`、`POST /api/reports/:id/options` |
| 导出 | `POST /api/reports/:id/export`、`GET /api/export-tasks`、`GET /api/export-tasks/:id`、`GET /api/export-tasks/:id/download` |
| 列配置 | `GET·PUT·DELETE /api/reports/:id/column-config` |
| 数据源 | `GET/POST/PUT/DELETE /api/datasources`、`POST /api/datasources/test`、`POST /api/datasources/:id/test` |
| 元数据 | `GET /api/datasources/:id/tables?keyword=&refresh=1&limit=`（返回 `{items,total,truncated,cached,ageSec}`）、`GET .../tables/:name/columns`、`GET .../tables/:name/indexes`、`POST /api/datasources/:id/meta/clear`（手动清该数据源缓存，DDL 后不必等 TTL） |
| 表生成报表 | `POST /api/report-wizard/generate`（只生成 SQL/列/条件草稿，不落库；保存仍走 `POST /api/reports`） |
| 字典 | `GET/POST/PUT/DELETE /api/dictionaries`、`GET /api/dictionaries/:id/types`、`POST /api/dictionaries/options` |
| 系统管理 | `/api/system/users`、`/api/system/roles`、`/api/system/menus`（含角色-菜单、角色-报表授权） |
| 操作日志 | `GET /api/system/audit-logs`、`GET /api/system/audit-logs/stats`、`GET /api/system/audit-logs/export`、`DELETE /api/system/audit-logs?before=YYYY-MM-DD` |
| 公开访问 | `GET /api/pub/:publicId/info`、`POST /api/pub/:publicId/query`（options、export 同路径）、`/api/pub/:publicId/column-config` |

响应格式统一为 `{ "code": 0, "msg": "ok", "data": ... }`，`code != 0` 时 `msg` 为可读错误信息。

---

## 九、部署

### 裸机（Linux）

```bash
tar -xzf report-system-linux.tar.gz -C /opt/report-system
cd /opt/report-system
vi config.yaml                 # 必须更换 jwt_secret / aes_key，必要时改端口
chmod +x report-server-amd64 start.sh stop.sh
./start.sh -d                  # 后台运行，日志 logs/server.log
curl http://127.0.0.1:18080/api/health
```

### systemd 常驻

```ini
# /etc/systemd/system/report-system.service
[Unit]
Description=Online Report System
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/report-system
ExecStart=/opt/report-system/report-server-amd64
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
systemctl daemon-reload && systemctl enable --now report-system
```

### Nginx 反向代理（可选）

```nginx
upstream report_server {
    server 127.0.0.1:18080;
    keepalive 32;
}

server {
    listen 80;
    server_name report.example.com;

    # 导出下载大文件：关闭缓冲，避免 ERR_CONTENT_LENGTH_MISMATCH
    proxy_buffering off;
    client_max_body_size 20m;

    location / {
        proxy_pass http://report_server;
        proxy_http_version 1.1;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 300s;
    }
}
```

> 常见 500 排查：`nginx` 的 `client_body_temp` / `proxy_temp` 目录归属必须与 worker 用户一致（如 worker 为 `admin`，则 `chown admin` 这两个目录），否则上传与代理会直接 500。

### Docker

```bash
cd backend/deploy
docker compose up -d            # 数据持久化在 deploy/data/
```

详见 [backend/deploy/README.md](backend/deploy/README.md)。

### 生产检查清单

- [ ] 已更换 `jwt_secret` 与 `aes_key`（或设置 `JWT_SECRET` / `AES_KEY` 环境变量）
- [ ] 已修改 admin 默认密码
- [ ] 对外访问走 HTTPS 反向代理
- [ ] 定期备份 `data/report_sys.db`（数据源、报表、用户配置都在这里）
- [ ] 公开报表的「允许导出」开关符合预期（默认关闭）
- [ ] `data/exports/` 磁盘容量充足（过期文件自动清理，默认 7 天）

---

## 十、常见问题（FAQ）

**Q1：忘记管理员密码怎么办？**

用内置工具生成 bcrypt 哈希，再写回系统库（`sys_user.password_hash`，同时清空锁定状态）：

```bash
cd backend
go run ./cmd/hashgen -p 新密码        # 省略 -p 则输出 admin123 的哈希
# 输出形如 $2a$10$xxxxxxxx...
```

```bash
# SQLite（未安装 sqlite3 时，可停服后用任意 SQLite 客户端打开 data/report_sys.db）
sqlite3 data/report_sys.db "UPDATE sys_user SET password_hash='<上面输出的哈希>', fail_count=0, locked_until=NULL WHERE username='admin';"
```

也可直接删除 `data/report_sys.db` 后重启（会清空数据源/报表/用户配置，仅建议空环境）。

**Q2：改了前端页面但刷新没变化？**

`go:embed` 是编译期快照。请重新 `npm run build` + `go build`（或直接跑 `build-and-run.bat`）。启动日志里的「嵌入产物概况」可确认是否为最新构建。

**Q3：启动日志提示「前端界面不可用」？**

说明 `backend/internal/webui/dist` 不存在。先在 `frontend/` 执行 `npm install && npm run build`，再重新编译后端。

**Q4：Oracle 连不上？**

- 纯 Go 驱动：只支持「主机 + 端口 + 服务名 / SID」，不支持 TNS 描述符串
- Windows CGO（godror）版：确认 `oci.dll`、`oraociei19.dll` 与 exe 同目录，并安装 VC++ 2015-2022 运行库
- 11g 数据源已适配分页（`ROWNUM` 三层嵌套），12c+ 使用 `OFFSET/FETCH`，无需手工干预

**Q5：端口被占用 / 想换端口？**

改 `config.yaml` 的 `server.port`，同时同步 Nginx `upstream` 与防火墙放行端口。

**Q6：一次查太多数据很慢 / 页面卡住？**

- `query.count_timeout_sec` 控制总数统计超时，超时会降级为「总数未知」，不阻塞数据返回
- 大结果集请用导出（自动转异步任务），而不是在页面翻到底
- `log.slow_query_ms` 可在日志里定位慢 SQL

**Q7：查询条件填了值却查不到数据？**

空值会被当作「未填」处理并回退到默认值；多值搜索可用换行或逗号分隔（如材料号批量粘贴）。`like` 类型条件不支持多值，请改用「等于」。

**Q8：跨模式表名怎么写？**

字典/数据源的跨模式表名统一写成 `模式名.表名`，如 `FGPESE0.SYS_DICT_TYPE`（后端会校验标识符合法性并按模式查询）。

---

## 十一、文档索引

| 文档 | 内容 |
|------|------|
| [docs/01-开发文档.md](docs/01-开发文档.md) | 需求、技术选型、模块设计、接口约定 |
| [docs/02-设计文档.md](docs/02-设计文档.md) | 数据库表结构、查询引擎与导出方案细节 |
| [docs/ui-design-preview.html](docs/ui-design-preview.html) | UI 设计预览（登录 / 报表中心 / 报表查询，含明暗主题） |
| [backend/deploy/README.md](backend/deploy/README.md) | Windows / Linux / Docker 部署细则 |
| `app/使用说明.txt` | 编译产物自带的分发说明（每次编译自动生成） |

---

## 十二、开发约定（摘要）

- **前端**：页面为 `.vue` 单文件组件；业务页根节点用 `.rs-page-fill` 撑满内容区，剩余高度交给表格内部滚动；Element Plus 组件按需使用，样式令牌统一走 `--rs-*`
- **主题**：明暗模式由 `html.dark` 驱动，Element 变量与自定义令牌同时切换；新增样式禁止写死颜色，统一用 `var(--rs-*)`
- **后端分层**：`api`（HTTP 编排）→ `service`（业务）→ `datasource`（驱动注册制），新增数据库类型实现 `Driver` 接口并在 `manager` 注册即可
- **SQL 处理**：参数占位符、字符串/注释掩码、Oracle 命名参数去重绑定均在 `service/query_engine.go` 与 `datasource/driver.go` 集中处理，改绑定逻辑前请先读这两处的分词状态机
- **提交前**：`go build ./...`、`go test ./...` 与前端 `npm run build` 均需通过
- **单测**：`service/report_wizard_test.go` 覆盖从表建报表的 SQL 生成（跨模式表名、中文别名、主键兜底排序、DISTINCT、无主键告警、注入拦截、500 列大宽表、SQL 超 60KB 报错）；生成器依赖的元数据能力已抽为 `wizardMeta` 接口，新增用例无需连数据库
