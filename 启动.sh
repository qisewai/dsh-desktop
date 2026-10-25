#!/usr/bin/env bash
# dsh-desktop 启动脚本
#
# 直接跑，或者双击本文件。第一次运行会自动编译（需要 Go）。
# 关掉窗口 = 退出，dsh 后端会一起被回收。

set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"

BIN="./dsh-desktop"

# 没有二进制，或者源码比二进制新 → 重新编译
need_build=0
if [ ! -x "$BIN" ]; then
    need_build=1
elif [ -n "$(find . \( -name '*.go' -o -name go.mod \) -not -path './dist/*' -newer "$BIN" -print -quit 2>/dev/null)" ]; then
    need_build=1
fi

if [ "$need_build" = "1" ]; then
    echo "正在编译 dsh-desktop ..."
    if ! command -v go >/dev/null 2>&1; then
        echo "找不到 go，无法编译。请先安装 Go，或直接使用已编译好的 $BIN" >&2
        exit 1
    fi
    # 沙箱/容器里 /tmp 常常很小，把构建临时目录指到大盘上更稳
    export GOTMPDIR="${GOTMPDIR:-$HOME/.cache/gotmp}"
    export TMPDIR="${TMPDIR:-$HOME/.cache/gotmp}"
    mkdir -p "$GOTMPDIR" "$TMPDIR"
    go build -o "$BIN" .
    echo "编译完成"
fi

exec "$BIN" "$@"
