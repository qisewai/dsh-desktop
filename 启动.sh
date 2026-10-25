#!/usr/bin/env bash
# dsh-desktop 启动脚本
#
# 直接跑，或者双击本文件。第一次运行会自动编译（需要 Go）。
# 关掉窗口 = 退出，dsh 后端会一起被回收。
#
# Windows（Git Bash，双击 .sh 默认就是它）：编成 GUI 程序（-H=windowsgui），在后台启动后脚本立即退出，
# Git Bash 窗口随之关掉。普通 go build 出来的是控制台程序，会一直挂着一个黑框，关掉黑框程序也跟着退出。
# 日志看 ~/.dsh-desktop/logs/app.log。

set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"

windows=0
case "$(uname -s)" in MINGW* | MSYS* | CYGWIN*) windows=1 ;; esac

BIN="./dsh-desktop"
ldflags=""
if [ "$windows" = "1" ]; then
    BIN="./dsh-desktop.exe"
    ldflags="-H=windowsgui"
fi

# 双击打开的 Git Bash 窗口一退出就关，出错时停一下，不然看不到报错
fail() {
    echo "$1" >&2
    if [ "$windows" = "1" ]; then
        read -r -p "按回车关闭" _ || true
    fi
    exit 1
}

# 没有二进制，或者源码比二进制新 → 重新编译
need_build=0
if [ ! -x "$BIN" ]; then
    need_build=1
elif [ -n "$(find . \( -name '*.go' -o -name go.mod \) -not -path './dist/*' -newer "$BIN" -print -quit 2>/dev/null)" ]; then
    need_build=1
fi

if [ "$need_build" = "1" ]; then
    echo "正在编译 dsh-desktop ..."
    command -v go >/dev/null 2>&1 || fail "找不到 go，无法编译。请先安装 Go，或直接使用已编译好的 $BIN"
    # 沙箱/容器里 /tmp 常常很小，把构建临时目录指到大盘上更稳
    export GOTMPDIR="${GOTMPDIR:-$HOME/.cache/gotmp}"
    export TMPDIR="${TMPDIR:-$HOME/.cache/gotmp}"
    mkdir -p "$GOTMPDIR" "$TMPDIR"
    go build -ldflags "$ldflags" -o "$BIN" . || fail "编译失败"
    echo "编译完成"
fi

if [ "$windows" = "1" ]; then
    # 用 cmd 的 start 脱离 Git Bash：窗口关掉时不会把程序一起带走
    cmd //c start "" "$(cygpath -w "$DIR/dsh-desktop.exe")" "$@"
    exit 0
fi
exec "$BIN" "$@"
