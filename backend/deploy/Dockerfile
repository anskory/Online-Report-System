# =============================================================================
# 在线报表系统 - 多阶段构建
# 构建示例（在 backend/ 目录执行）:
#   cd backend && docker build -f deploy/Dockerfile -t report-system:latest .
#
# 构建参数:
#   WITH_ORACLE=1  启用 Oracle 支持（默认 0；需要 CGO，构建较慢）
# =============================================================================

# ---------- 构建阶段 ----------
FROM golang:1.22-bookworm AS builder

ARG WITH_ORACLE=0
WORKDIR /build

# 国内网络可打开下一行注释走 goproxy.cn
# ENV GOPROXY=https://goproxy.cn,direct

COPY go.mod go.sum ./
# go mod tidy 补齐 go.sum（新增依赖如纯 Go Oracle 驱动 go-ora 首次构建时需要）
RUN go mod download && go mod tidy

COPY . .

# 依赖检查：启用 Oracle 时需在构建环境安装 gcc + pkg-config + Oracle Instant Client SDK
# （instantclient SDK 挂到 /usr/lib/instantclient，并设 PKG_CONFIG_PATH）
RUN if [ "$WITH_ORACLE" = "1" ]; then \
      export CGO_ENABLED=1 && \
      go build -tags oracle -ldflags "-s -w" -o /out/report-server ./cmd/server ; \
    else \
      export CGO_ENABLED=0 && \
      go build -ldflags "-s -w" -o /out/report-server ./cmd/server ; \
    fi

# ---------- 运行阶段 ----------
FROM debian:bookworm-slim

ARG WITH_ORACLE=0
ENV TZ=Asia/Shanghai

WORKDIR /app
COPY --from=builder /out/report-server /app/report-server
# 仓库只提交示例配置（真实密钥不入库）；生产请挂载自有 config.yaml 或用 JWT_SECRET / AES_KEY 环境变量覆盖
COPY config.yaml.example /app/config.yaml

# Oracle 支持：运行期需要 Instant Client Basic 包
# 将 instantclient_*_linux.zip 预先放到 deploy/libs/ 目录（勿提交仓库）
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates tzdata libaio1 \
    && rm -rf /var/lib/apt/lists/*
COPY deploy/libs/instantclient/ /opt/oracle/instantclient/
ENV LD_LIBRARY_PATH=/opt/oracle/instantclient

EXPOSE 18080
VOLUME ["/app/data"]

# 单机单服务：SQLite 嵌入式系统库，数据落在 /app/data
CMD ["./report-server", "-config", "config.yaml"]
