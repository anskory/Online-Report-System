#!/usr/bin/env bash
# 构建脚本（Linux/macOS）
# 用法：./build.sh            # 标准构建（不含 Oracle）
#       ./build.sh --oracle   # 含 Oracle 支持（需 gcc + Oracle Instant Client）
set -e
export GOPROXY="goproxy.cn,direct"

# ---------- 前端构建（Vue 3 + Vite）：产物输出到 internal/webui/dist 供 go:embed 使用 ----------
FRONTEND="$(cd "$(dirname "$0")/.." && pwd)/frontend"
if [ -f "$FRONTEND/package.json" ]; then
    cd "$FRONTEND"
    if [ ! -d node_modules ]; then
        echo "[前端] 首次构建，安装依赖 ..."
        npm install
    fi
    echo "[前端] vite build ..."
    npm run build
    cd - >/dev/null
else
    echo "[前端] 未找到 frontend/package.json，跳过前端构建"
fi

# 同步依赖（补齐 go.sum：新增依赖如纯 Go Oracle 驱动 go-ora 首次构建时需要）
go mod tidy

if [ "$1" = "--oracle" ]; then
    export CGO_ENABLED=1
    go build -tags oracle -o report-server ./cmd/server
else
    export CGO_ENABLED=0
    go build -o report-server ./cmd/server
fi
echo "构建完成: report-server"
