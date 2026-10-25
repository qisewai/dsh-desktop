#!/usr/bin/env bash
# 打包：生成图标 → mygo build → AppImage，产物都在 dist/linux-amd64/
#
#   DSH_Desktop-<版本>-x86_64.AppImage                    单文件，chmod +x 后直接跑（推荐）
#   dsh-desktop-<版本>-linux-amd64.tar.gz + install.sh     用户级安装到 ~/.local
#   dsh-desktop_<版本>_amd64.deb                           Debian/Ubuntu
#
# 版本号在 mygo.json 里改。
#
# AppImage 是「瘦」的：只装了本程序，GTK / WebKitGTK 4.1 / appindicator 用系统的（程序运行时才
# dlopen 它们，本来就不链接），dsh 本体和 Node 也得系统里有——这个壳本来就是给系统里的 dsh 用的。
# 把 WebKitGTK 塞进 AppImage 要带上它写死路径的辅助进程，体积从 4MB 涨到上百 MB，不值得。
#
# AppImage 的运行时（squashfs 前面那段 ELF）默认取 ~/.cache/dsh-desktop/runtime-x86_64，没有的话：
#   ./打包.sh --runtime-from 某个现成的.AppImage     从已有的 AppImage 里切出来（只读文件，不执行它）
#   或者从 https://github.com/AppImage/type2-runtime/releases 下载 runtime-x86_64 放到上面的路径
# 要用新版静态运行时（不依赖 libfuse2，支持 zstd）。
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
export GOTMPDIR="${GOTMPDIR:-$HOME/.cache/gotmp}" TMPDIR="${TMPDIR:-$HOME/.cache/gotmp}"
mkdir -p "$GOTMPDIR"

RUNTIME="${APPIMAGE_RUNTIME:-$HOME/.cache/dsh-desktop/runtime-x86_64}"
mygo_args=()
while [ $# -gt 0 ]; do
	case "$1" in
	--runtime-from)
		src="$2"
		shift 2
		# AppImage = 运行时 ELF + squashfs；运行时的长度 = 节头表结束的位置
		size=$(python3 -c '
import struct, sys
h = open(sys.argv[1], "rb").read(64)
assert h[:4] == b"\x7fELF" and h[4] == 2, "不是 64 位 ELF"
shoff, = struct.unpack("<Q", h[0x28:0x30])
shentsize, shnum = struct.unpack("<HH", h[0x3a:0x3e])
print(shoff + shentsize * shnum)' "$src")
		[ "$(tail -c +"$((size + 1))" "$src" | head -c 4)" = "hsqs" ] || { echo "$src 里在 $size 处没找到 squashfs，不是 type2 AppImage" >&2; exit 1; }
		mkdir -p "$(dirname "$RUNTIME")"
		head -c "$size" "$src" >"$RUNTIME"
		chmod +x "$RUNTIME"
		echo "运行时取自 $src（$size 字节）→ $RUNTIME"
		;;
	*)
		mygo_args+=("$1")
		shift
		;;
	esac
done

go run ./tools/genicon
go tool mygo build "${mygo_args[@]}"

# ---- AppImage ----
out=dist/linux-amd64
version=$(python3 -c 'import json; print(json.load(open("mygo.json"))["version"])')
if [ ! -s "$RUNTIME" ]; then
	echo "跳过 AppImage：没有运行时 $RUNTIME（见脚本开头的说明）" >&2
	exit 0
fi
command -v mksquashfs >/dev/null || { echo "跳过 AppImage：没有 mksquashfs（装 squashfs-tools）" >&2; exit 0; }

appdir=$(mktemp -d "$TMPDIR/dsh-desktop.AppDir.XXXXXX")
trap 'rm -rf "$appdir"' EXIT
mkdir -p "$appdir/usr/bin" "$appdir/usr/share/icons/hicolor/256x256/apps" "$appdir/usr/share/applications"
cp "$out/dsh-desktop" "$appdir/usr/bin/"
go run ./tools/genicon "$appdir/usr/share/icons/hicolor/256x256/apps/dsh-desktop.png" 256
cp "$appdir/usr/share/icons/hicolor/256x256/apps/dsh-desktop.png" "$appdir/dsh-desktop.png"
ln -s dsh-desktop.png "$appdir/.DirIcon"
# AppRun 用 exec -a 把 argv[0] 固定成 dsh-desktop：AppImage 运行时传进来的 argv[0] 是镜像文件名，
# GTK 拿它当程序名，窗口的 WM_CLASS / Wayland app_id 就成了「DSH_Desktop-0.1.1-x86_64.AppImage」，
# 任务栏对不上桌面条目（StartupWMClass），换个版本号还会变。
# dsh:// 注册不受影响：MyGo 在 AppImage 里用 $APPIMAGE（镜像文件本身）而不是挂载路径。
cat >"$appdir/AppRun" <<'APPRUN'
#!/bin/bash
here="$(dirname "$(readlink -f "$0")")"
exec -a dsh-desktop "$here/usr/bin/dsh-desktop" "$@"
APPRUN
chmod +x "$appdir/AppRun"
cat >"$appdir/dsh-desktop.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=DSH Desktop
Comment=DeepSeek Harness 桌面壳
Exec=dsh-desktop %u
Icon=dsh-desktop
Categories=Development;
Terminal=false
MimeType=x-scheme-handler/dsh;
StartupWMClass=dsh-desktop
X-AppImage-Version=$version
EOF
cp "$appdir/dsh-desktop.desktop" "$appdir/usr/share/applications/"

image="$out/DSH_Desktop-$version-x86_64.AppImage"
squash="$appdir.squashfs"
mksquashfs "$appdir" "$squash" -root-owned -noappend -comp zstd -Xcompression-level 19 -quiet
cat "$RUNTIME" "$squash" >"$image"
rm -f "$squash"
chmod +x "$image"
echo "AppImage: $image（$(du -h "$image" | cut -f1)）"
