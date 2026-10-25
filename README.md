# dsh-desktop

给 DeepSeek Harness 做的极简桌面壳。用 MyGo（纯 Go，无 cgo）开原生窗口，加载 dsh 自己的 Web UI。

**不自研界面** —— dsh 本来就是个本地 HTTP 服务，壳层只负责把它拉起来、拿到带 token 的地址、开个窗口装进去。

## 安装

```bash
./打包.sh       # 生成图标 + mygo build + AppImage，产物在 dist/linux-amd64/
```

### AppImage（推荐）

`dist/linux-amd64/DSH_Desktop-<版本>-x86_64.AppImage`，单文件约 4MB，放到固定位置（比如 `~/Applications/`）后直接运行。装了 AppImageLauncher 的话，第一次打开它会问要不要集成到应用菜单。

这是个**瘦** AppImage：只装了本程序，GTK、WebKitGTK 4.1、appindicator 用系统的（程序运行时才 dlopen 它们），dsh 本体和 Node 也得系统里有——这个壳本来就是给系统里的 dsh 用的。所以能在装了 `webkit2gtk-4.1` 的发行版上跑（Manjaro/Arch、Fedora、Ubuntu 22.04+ 等）。把 WebKitGTK 塞进 AppImage 要带上它写死路径的辅助进程，体积会涨到上百 MB，不值得。

AppImage 第一次运行时把 `dsh://` 注册到镜像文件本身的路径（MyGo 用 `$APPIMAGE`），所以**挪了位置或换了版本后要运行一次**，链接才会指过去。

打包需要 `mksquashfs`（squashfs-tools）和 AppImage 运行时 `~/.cache/dsh-desktop/runtime-x86_64`。运行时没有的话：`./打包.sh --runtime-from 某个现成的.AppImage` 从已有的 AppImage 里切出来（只读文件、不执行），或者从 [AppImage/type2-runtime](https://github.com/AppImage/type2-runtime/releases) 下载 `runtime-x86_64`。要新版静态运行时（不依赖 libfuse2、支持 zstd）。

### tar.gz / deb

```bash
sh dist/linux-amd64/install.sh                 # 用户级安装到 ~/.local，不要 root
sh dist/linux-amd64/install.sh --uninstall     # 卸载（设置、窗口状态、日志保留）
```

装到 `~/.local/dsh-desktop.app`，命令 `~/.local/bin/dsh-desktop`，应用菜单里叫「DSH Desktop」（开发类）。`.deb` 给 Debian/Ubuntu 用。

AppImage 和 install.sh 装的版本别同时用：它们共用设置和单实例锁，`dsh://` 也会互相抢（谁最后启动归谁）。

版本号在 `mygo.json` 里改。打包用的 `mygo` 命令行工具已经作为 `go tool` 写进 `go.mod`，不用另装。注意 `mygo build` 每次会清空 `dist/linux-amd64/`。

### Windows

```powershell
powershell -ExecutionPolicy Bypass -File .\打包.ps1    # 生成图标 + mygo build，产物在 dist\windows-amd64\
```

- `DSH Desktop Setup <版本> amd64.exe`：NSIS 安装包，装到 `%LOCALAPPDATA%\Programs\DSH Desktop`，不要管理员权限，建开始菜单快捷方式、注册 `dsh://`，在「设置 → 应用」里可卸载。
- `DSH Desktop.exe`：GUI 程序本体（没有控制台窗口），也能直接拷走用。

没装 NSIS 时 `mygo build` 第一次会自己下载到 `%LOCALAPPDATA%\mygo`。dsh 用 npm 全局装的那个（`%APPDATA%\npm\dsh.cmd`，或 PATH 里的 `dsh`），需要系统里有 Node；窗口用系统自带的 WebView2（Windows 10/11 一般都有）。

## 开发

```bash
./启动.sh        # 源码比二进制新就自动重编译，然后启动
go test ./...
```

Windows 上直接双击 `启动.sh`（装了 Git for Windows 时 `.sh` 默认由 Git Bash 打开；双击 `.ps1` 默认只会用记事本打开），或者在终端里跑 `powershell -ExecutionPolicy Bypass -File .\启动.ps1`。两者都会按需重编译，编成 GUI 程序（`-H=windowsgui`）后在后台启动、脚本立即返回，日志看 `app.log`。直接 `go build` 出来的是控制台程序，启动会带一个黑框，关掉黑框程序也退出。两个 `.ps1` 是带 BOM 的 UTF-8（Windows PowerShell 5.1 不认无 BOM 的 UTF-8，中文会乱码），改的时候别把 BOM 弄丢。

开发版和安装版共用同一份设置和窗口状态（`~/.config/dsh-desktop/`，Windows 是 `%APPDATA%\dsh-desktop\`），也共用单实例锁：同时只能开一个。

## 功能

启动时窗口会立刻出现并显示加载进度；dsh 就绪后自动切到它的界面。启动失败时窗口里显示报错页（含 dsh 最近的输出和日志路径），点「重试」重新拉起，不用关窗口。

dsh 运行中崩溃会自动重启（间隔 1s/2s/4s，尽量复用原端口）；2 分钟内连崩 4 次就停下来显示报错页，等你点「重试」——防止配置/插件坏了时无限重启。启动阶段的失败不自动重试（通常是确定性的，比如找不到 dsh、插件安装报错）。

**关窗口 = 收进托盘**，dsh 继续在后台跑（第一次会弹通知提醒）。托盘菜单：显示窗口 / 隐藏窗口 / Profile / 重启 dsh / 通知开关 / dsh:// 开关 / 退出。只有托盘「退出」或 SIGTERM 才真正退出并回收 dsh。托盘不可用（没装 `libayatana-appindicator3`）或加了 `-tray=false` 时，关窗口就是退出。

**切换 profile**：托盘菜单「Profile：xxx」子菜单，列出 `$DSH_HOME/profiles`（默认 `~/.dsh/profiles`）下能出 Web UI 的 profile（`package.json` 的 bundles 含 `@deepseek-ai/dsh-web-app`；`desktop` 归 Electron 版管，不列）。选中后立即重启 dsh，选择存进 `~/.config/dsh-desktop/settings.json`，下次启动沿用。列表在每次 dsh 启动成功后刷新。

**版本检查**：每次启动 dsh 前跑 `dsh --version`。低于 `0.1.7-rc.1` 直接显示报错页（升级后点重试）；主.次版本比验证过的 `0.2.1-rc.2` 新时照常启动，但加载页上会提示，卡在启动阶段时报错里也附上这条——dsh 改了 token 输出格式时最先坏的就是这里。两个版本号在 `version.go` 顶部。

**系统通知**：一轮完成、等你审批、等你回答提问时弹系统通知，标题带会话名，正文是回复 / 审批原因 / 问题；点通知把窗口调出来。只在窗口隐藏或不在前台时弹；你自己停掉的那轮、子代理会话不提醒。托盘里可关，存进 `settings.json`。

**托盘图标反映状态**：右下角加一个圆点——绿：有会话在跑；橙：有审批或提问在等你；红：dsh 没在正常运行（报错页、连崩停摆）；没点：空闲。优先级红 > 橙 > 绿。托盘菜单顶上多一行状态（如「● 1 个等你审批 · 2 个会话运行中」，点它调出窗口），悬停提示里也有。数据和通知同源（会话事件日志）；dsh 重启时全部清零（被打断的轮次不会写 `turn/end`），运行中的会话 30 分钟没有新事件也当它停了，等你处理的不过期。

**dsh:// 链接**：和官方 Electron 版、DSH Tauri 用同一个 scheme，只认 `dsh://open`（把窗口调出来，DeepSeek 平台登录成功页的「打开应用」落在这里），其他地址忽略。**只有安装版会自动注册**（写 `~/.local/share/applications/com.mygo.dshdesktop.url-handler.desktop`，并在 `~/.config/mimeapps.list` 设为 `x-scheme-handler/dsh` 的默认处理程序）；开发版不自动注册，免得和安装版来回抢默认。托盘里取消勾选会撤销注册。

窗口位置、大小、最大化状态会记住（`~/.config/dsh-desktop/window-state.json`），下次启动恢复。

再次运行时如果已有实例，会把已有窗口（包括藏在托盘里的）提到前台，自己直接退出。

### 可选参数

```bash
dsh-desktop -profile tauri       # 本次用指定 profile（不写回设置；默认用上次托盘里选的，没有就 web）
dsh-desktop -port 18300          # 指定端口（默认从 18200 起自动找空闲）
dsh-desktop -width 1600 -height 1000  # 只影响首次启动，之后用记住的大小
dsh-desktop -tray=false          # 不要托盘，关窗口即退出
dsh-desktop -skip-version-check  # 不检查 dsh 版本
dsh-desktop -dsh /path/to/dsh    # 指定 dsh 可执行文件
```

日志：`~/.dsh-desktop/logs/app.log`（同时在终端输出）

## 启动器里埋的坑

改代码时别删，每个都对应一条不明显的失败现象：

| 处理 | 不这么做会怎样 |
|---|---|
| `dsh --profile <name> --port … --no-open`，`--profile` 放最前 | 写成 `dsh web … --profile x` 时，`--profile` 被透传给 web 应用，dsh 报 `unknown option '--profile'` 直接退出（早期版本的 `-profile` 参数就是这么坏的） |
| `stdbuf -oL -eL` 启动 dsh（Linux） | dsh 的 stdout 是块缓冲，接管道时 token 那行**永远读不到**，等到超时。终端里手动跑却正常，极难复现。Windows 没有 stdbuf，也不需要（实测直接能读到） |
| 轮询到 `303 + Set-Cookie` 才算就绪 | dsh 先 bind 端口再拉后端，中间请求全是 502。只看端口 open 会打开一个能渲染但连不上后端的页面 |
| 启动前补 `NO_PROXY` | 环境里只有 `HTTP_PROXY` 时，WebKit 把 `ws://127.0.0.1` 也走代理：HTTP 能转发、WebSocket 握手被吞，界面正常但一直重连、发不出消息 |
| 独立进程组 + 负 PID 回收 | dsh 带一串 node 子进程，只杀父进程会留孤儿占着端口和 profile 锁。启动失败、dsh 自己崩了也要回收整组 |
| Windows：dsh 放进 Job Object（`KILL_ON_JOB_CLOSE`），回收 = 终止整个 job | Windows 没有进程组信号；npm 装的 `dsh.cmd` 是 cmd.exe → node 两层，只杀 cmd.exe 会留下 node。job 句柄由壳持有，壳被任务管理器强杀时系统关句柄，dsh 也跟着走 |
| Windows：子进程带 `CREATE_NO_WINDOW` | 打包版是 GUI 程序，没有控制台；cmd.exe / node 启动时会各自弹一个黑窗口 |
| 拿到 token 后继续读 stdout | 不读的话管道 64KB 写满，dsh 往 stdout 写日志时阻塞，用一阵子后界面突然卡死。输出用 `os.Pipe` 自管，别用 `StdoutPipe`（node 子进程继承写端，Wait 语义会出问题） |
| 托盘对象存进包级变量 `appTray` | MyGo 的菜单注册表只存**弱引用**，Linux 托盘后端也不持有 Go 侧的 `*Tray`。不留引用的话 GC 一跑菜单项就没了：刚启动时点菜单正常，过一会儿点了静默无反应 |
| AppImage 的 AppRun 用 `exec -a dsh-desktop` | AppImage 运行时传进来的 argv[0] 是镜像文件名，GTK 拿它当程序名：窗口 WM_CLASS / Wayland app_id 成了 `DSH_Desktop-0.1.1-x86_64.AppImage`，任务栏对不上桌面条目、显示成通用图标，换版本号还会变 |
| 通知读会话事件日志，不读 Web UI 也不轮询 `/api/session/list` | 会话状态只在 dsh 前端的 Cordis 上下文里，注入脚本拿不到；`session/list` 有 `running` 却**没有待审批**。`session.v4.jsonl.zstd` 每次提交追加一个独立 zstd 帧，按偏移只解新增部分 |

## 已知限制

- **单实例**：用 MyGo 的 `RequestSingleInstanceLock`（`$XDG_RUNTIME_DIR` 下的 unix socket）。第二个实例的普通参数只记日志、不生效——想换 profile/端口得先关掉已有窗口；`dsh://` 链接会转交给已有实例处理。
- **Windows 上回收 dsh 是直接终止**：没有 SIGTERM，没控制台的 node 也收不到 Ctrl+Break，dsh 没有机会自己收尾。只在本机（Windows 10 LTSC 21H2 + dsh 0.2.0-rc.2）验证过启动、加载界面、强杀壳时 dsh 一起退出；托盘、通知、`dsh://`、安装包装好后的行为没逐项实测。
- **端口动态**：JWT 里的 `authority` 绑定了端口，换端口旧 cookie 立即失效，所以每次启动都要重新走 token。
- **托盘（Linux）**：AppIndicator 没有单击事件，点图标只会弹菜单，所以「显示窗口」放在菜单第一项。
- **窗口位置（Wayland）**：Wayland 不允许应用自己摆位置，原生 Wayland 下只能记住大小和最大化状态，位置由合成器决定（没在本机专门验证）。
- **通知**：读的是整个 `$DSH_HOME/sessions`，所以在别处跑的 dsh（比如命令行 `dsh tui`）的会话也会触发；点通知只能调出窗口，跳不到具体会话（dsh 的界面打开会话时 URL 不变）；依赖 dsh 的持久化格式 v4 和事件名（`turn/end`、`approval/asked`、`ask_user_question`），dsh 改格式时通知会静默失效。点击通知调出窗口这一步没有实测到（通知确实发到了 KDE）。
- **版本下限没实测**：`0.1.7-rc.1` 取自 DSH Tauri 桌面版声明的最低内核版本，本壳只在 `0.2.1-rc.2` 上验证过。
- **AppImage**：瘦包，依赖系统的 WebKitGTK 4.1；只验证过本机（Manjaro + KDE）。没有自动更新。
- **未做**：全局快捷键、自动更新、崩溃上报。

## 结构

```
main.go        窗口、单实例、启动/崩溃重启/重试的守护循环
dsh.go         dsh 进程：启动、输出泵、等 token / 就绪、进程组回收
proc_unix.go   Linux/macOS：stdbuf、进程组
proc_windows.go  Windows：Job Object、隐藏控制台、dsh.cmd 查找
pages.go       加载页、报错页（本地 HTML）
tray.go        托盘菜单（状态行、profile 子菜单、通知 / dsh:// 开关）、关窗收进托盘的提示
status.go      托盘状态：会话计数 + dsh 是否在跑 → 角标颜色、状态行
profiles.go    profile 列表、当前 profile、settings.json 读写
version.go     dsh 版本解析与兼容性检查
notify.go      会话事件日志 → 系统通知 + 各会话的运行/等待状态
deeplink.go    dsh:// 注册与处理
*_test.go      单测（版本比较、通知解析、状态计数、角标）
internal/appicon/  代码画的图标与状态角标（窗口、托盘、打包共用）
tools/genicon/     生成 resources/icon.png
mygo.json      打包配置（名字、版本、桌面条目、deb 依赖）
启动.sh        开发启动：源码比二进制新就自动重编译
打包.sh        打包：生成图标 + mygo build + AppImage → dist/
启动.ps1 / 打包.ps1  Windows 版的开发启动 / 打包
```

依赖：`github.com/egoist/mygo v0.6.3`、`github.com/klauspost/compress`（解 zstd）、dsh 本体（`/usr/bin/dsh`）、WebKitGTK 运行时；托盘可选依赖 `libayatana-appindicator3`。

## 待改进

- 点通知直接打开对应会话；通知上直接「批准 / 拒绝」
- 通知只看本壳拉起的 dsh 的会话
- 全局快捷键、窗口缩放快捷键、`app.log` 轮转
