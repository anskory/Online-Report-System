# 部署说明（Windows / Linux / Docker）

## 一、产物形态

单机单服务：**1 个可执行文件 + 1 个 config.yaml + data 目录**。
前端（Vue3 + ElementPlus 免构建版）已经 `go:embed` 打进二进制，无静态文件部署。

```
report-server.exe (或 report-server)
config.yaml
data/              # 自动创建：report_sys.db（系统库）、exports/（导出文件）
```

## 二、构建矩阵

| 目标平台 | 构建方式 | Oracle 支持 |
|----------|----------|-------------|
| Windows x64 | `build.bat`（本机构建） | `build.bat oracle`（需 MinGW-w64 + Instant Client） |
| Linux x64 | 在 Linux 上执行 `build.sh` | `CGO_ENABLED=1 go build -tags oracle`（需 gcc + Instant Client SDK） |
| Docker | `docker build -f deploy/Dockerfile .` | 构建参数 `WITH_ORACLE=1` |

> **godror（Oracle 驱动）依赖 CGO，无法简单交叉编译**。Windows↔Linux 交叉构建时请去掉 Oracle 支持
> （默认构建即不含 Oracle），或在目标平台本机/Docker 内构建。

### Windows 构建（最简）

```bat
build.bat            :: 产出 report-server.exe（不含 Oracle）
build.bat oracle     :: 含 Oracle，前提：已装 MinGW-w64 并设 PKG_CONFIG_PATH 指向 Instant Client SDK
```

### Linux 构建

```bash
./build.sh                                # 不含 Oracle
CGO_ENABLED=1 go build -tags oracle -o report-server ./cmd/server   # 含 Oracle
```

运行期 Oracle 需 Instant Client Basic：解压到 `/opt/oracle/instantclient`
并 `export LD_LIBRARY_PATH=/opt/oracle/instantclient`（Windows 是加入 PATH）。

## 三、部署步骤（裸机）

1. 拷贝 `report-server(.exe)` + `config.yaml` 到目标机器
2. 修改 `config.yaml`：**必须更换 `jwt_secret` 与 `aes_key`**（32 字节以上随机串）
3. 运行：
   - Windows：双击 `start-server.bat`（或 `report-server.exe -config config.yaml`）
   - Linux：`chmod +x report-server && ./report-server -config config.yaml`
4. 停止：`stop-server.bat [端口]`（Windows）/ `kill $(pgrep report-server)`（Linux）
5. 首次启动自动建库建表，初始账号 `admin / admin123`，**登录后立即改密**

## 四、Docker 部署

```bash
cd backend/deploy
# 不含 Oracle：
docker compose up -d
# 含 Oracle：先按 Dockerfile 注释把 Instant Client Basic 解压到 deploy/libs/instantclient/，
# 再把 docker-compose.yml 中 WITH_ORACLE 改为 "1" 后 up
```

数据持久化在 `deploy/data/`（SQLite + 导出文件）。

## 五、生产检查清单

- [ ] `jwt_secret` / `aes_key` 已更换（环境变量 JWT_SECRET / AES_KEY 可覆盖配置文件）
- [ ] admin 默认密码已修改
- [ ] 反向代理（Nginx 等）启用 HTTPS 后再对外
- [ ] 公开报表确认 `PubAllowExport` 开关符合预期（默认关闭导出）
- [ ] `data/exports/` 所在磁盘容量充足（异步导出文件按保留期自动清理，默认 7 天）
- [ ] 定期备份 `data/report_sys.db`（报表/数据源/用户配置都在里面）
