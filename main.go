// dsh-desktop：给 DeepSeek Harness 做的极简桌面壳。
//
// 思路：不自研界面，直接加载 dsh 自己的 Web UI（它本来就是个本地 HTTP 服务）。
// 壳层只负责三件事：把 dsh 拉起来、拿到带 token 的地址、开个原生窗口装进去。
//
// 启动器里踩过的坑都已经写进代码，改动时别删注释里的原因说明：
//  1. stdbuf 强制行缓冲，否则读不到 token
//  2. 轮询到 303 才算就绪，端口 open 时还是 502
//  3. NO_PROXY 必须补，否则 WebSocket 被代理吞掉
//  4. 独立进程组，退出时整组回收
//  5. 拿到 token 后 stdout 也要一直读，否则管道写满 dsh 卡死
//
// 文件：main.go 窗口与生命周期，dsh.go 进程管理，pages.go 加载页/报错页，tray.go 托盘与图标，
// profiles.go profile 列表与持久化，version.go dsh 版本兼容检查，
// notify.go 会话事件 → 系统通知，deeplink.go dsh:// 链接。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dshdesktop/internal/appicon"

	"github.com/egoist/mygo"
)

var (
	dshBin  = flag.String("dsh", "", "dsh 可执行文件路径（默认自动查找）")
	profile = flag.String("profile", "", "dsh profile 名（默认用上次在托盘里选的，没有就用 web）")
	skipVer = flag.Bool("skip-version-check", false, "不检查 dsh 版本兼容性")
	portArg = flag.Int("port", 0, "指定端口（默认自动从 18200 起找空闲）")
	width   = flag.Int("width", 1280, "窗口宽度")
	height  = flag.Int("height", 820, "窗口高度（只在首次启动时用，之后记住上次的大小和位置）")
	useTray = flag.Bool("tray", true, "显示托盘图标，关窗口时收进托盘而不是退出")
)

// 当前的 dsh 进程。退出时由 main 回收；quitting 之后再启动的进程会被立刻回收。
var current struct {
	sync.Mutex
	proc     *dshProc
	quitting bool
}

func main() {
	flag.Parse()

	// 必须在创建窗口之前调用：WebKit 是这里 fork 出来的子进程，继承本进程的环境变量。
	// 环境里只有 HTTP_PROXY 而没有 NO_PROXY 时，WebKit 会把 ws://127.0.0.1 也走代理，
	// HTTP 能转发过去、WebSocket 的 Upgrade 握手却被吞掉，表现为界面正常但一直重连。
	ensureNoProxyForLoopback()

	logPath := setupLog()
	log.Printf("=== 启动 dsh-desktop，日志 %s ===", logPath)

	// 单实例：两个壳抢同一个 profile 会撞 .plugin-manager 的写锁。
	// 第二个实例把参数转给第一个、由它把窗口提到前台，然后自己退出。
	mygo.App.SetName("dsh-desktop")
	if !mygo.App.RequestSingleInstanceLock() {
		log.Println("已有实例在运行，已激活其窗口")
		return
	}
	initProfile(*profile)
	log.Printf("profile: %s", activeProfile())

	ctx, cancel := context.WithCancel(context.Background())
	var win *mygo.Window

	mygo.App.OnSecondInstance(func(args []string, _ string) {
		// 参数里的 dsh:// 链接 MyGo 会另外交给 OnOpenURL
		log.Printf("第二个实例启动（参数 %q），激活窗口", args)
		showWindow(win)
	})
	setupURLScheme(func() { showWindow(win) })
	notifyEnabled.Store(boolOr(loadSettings().Notify, true))
	mygo.App.OnNotificationClick(func(id string) {
		log.Printf("点击通知 %s，激活窗口", id)
		showWindow(win)
	})

	// 真正退出（托盘「退出」、SIGTERM）时 OnBeforeQuit 先于各窗口的 OnClose 触发，
	// 靠这个标记区分「点了关闭按钮 → 收进托盘」和「要退出了 → 放行」。都在主线程。
	appQuitting := false
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { appQuitting = true })

	mygo.App.WhenReady(func() {
		// 先开窗口显示加载页，dsh 就绪后再切过去，不让用户对着空白等几秒
		win = mygo.NewWindow(mygo.WindowOptions{
			Title:  "DeepSeek Harness",
			Width:  *width,
			Height: *height,
			// 记住位置、大小、最大化状态：存在 MyGo 的 PathUserData/window-state.json，
			// 关窗口和退出时保存。显示器拔掉导致位置不可见时 MyGo 会自动忽略。
			StateKey: "main",
		})
		icon := appicon.PNG(64)
		_ = win.SetIcon(icon)
		// 报错页的「重试」是个指向 retryURL 的链接：在这里拦下导航、通知 supervise。
		// 比 JS 绑定简单，也不用给 about:blank 开信任。LoadURL 不触发这个回调。
		// 消息是下次加载页上的说明（如「切换到 profile x」），报错页的重试发空串
		retry := make(chan string, 1)
		win.Page().OnWillNavigate(func(e *mygo.NavigateEvent) {
			if e.URL != retryURL {
				return
			}
			e.PreventDefault()
			select {
			case retry <- "":
			default: // 连点只算一次
			}
		})

		requestRestart := func(note string) {
			select {
			case retry <- note:
			default:
			}
		}
		if *useTray && setupTray(win, requestRestart) {
			win.OnClose(func(e *mygo.CloseEvent) {
				if appQuitting {
					return
				}
				e.PreventDefault()
				win.Hide()
				noticeHiddenToTray()
			})
		}
		log.Printf("窗口已创建: id=%d", win.ID())
		startNotifier(win, ctx.Done())
		go supervise(ctx, win, retry, logPath)
	})

	// 窗口关闭 / SIGINT / SIGTERM（mygo 自己接管信号转成 Quit）→ App.Run 返回
	if err := mygo.App.Run(); err != nil {
		log.Printf("窗口异常: %v", err)
	}
	log.Println("窗口已关闭，清理")
	cancel()
	current.Lock()
	current.quitting = true
	p := current.proc
	current.Unlock()
	if p != nil {
		p.Stop()
	}
}

// 崩溃自动重启的节奏：间隔依次拉长；窗口期内崩太多次就不再自动拉，交给用户点「重试」，
// 免得 dsh 一启动就崩（配置写坏、插件报错）时无限循环刷日志。
var (
	restartDelays = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	crashWindow   = 2 * time.Minute
)

// bootError 是一次启动中失败的步骤，title 显示为报错页标题。
type bootError struct {
	title string
	err   error
}

func (e *bootError) Error() string { return e.title + ": " + e.err.Error() }
func (e *bootError) Unwrap() error { return e.err }

// supervise 拉起 dsh 并守护它：崩了自动重启，启动失败或崩得太频繁就显示报错页，
// 等用户点「重试」（retry 收到信号）再来。dsh 正常运行时 retry 来信号（托盘「重启 dsh」）
// 就主动重启一次。只在 ctx 取消（窗口关闭）时返回。
func supervise(ctx context.Context, win *mygo.Window, retry <-chan string, logPath string) {
	port := *portArg
	var crashes []time.Time
	note := ""

	for {
		p, usedPort, err := bootOnce(ctx, win, port, note)
		if usedPort != 0 {
			port = usedPort // 重启时优先复用同一端口
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return // 用户在启动过程中关了窗口
			}
			title, cause := "启动失败", err
			var be *bootError
			if errors.As(err, &be) {
				title, cause = be.title, be.err
			}
			tail := ""
			if p != nil {
				tail = p.Tail()
			}
			var ok bool
			if note, ok = waitRetry(ctx, win, retry, title, cause, tail, logPath); !ok {
				return
			}
			crashes = nil
			continue
		}

		// 启动期间点的「重启」作废：刚起来又立刻重启没有意义
		select {
		case <-retry:
		default:
		}

		// 守护 dsh：中途挂了就回收残留子进程，然后按节奏重启
		select {
		case <-ctx.Done():
			return
		case msg := <-retry:
			if msg == "" {
				msg = "手动重启"
			}
			log.Printf("重启 dsh：%s", msg)
			p.Stop()
			crashes, note = nil, msg
			continue
		case <-p.exited:
		}
		if p.stopping.Load() {
			return
		}
		crashErr := p.exitError()
		log.Printf("dsh 意外退出：%v", crashErr)
		p.Stop() // 主进程没了，同组的 node 子进程可能还在

		now := time.Now()
		recent := crashes[:0]
		for _, t := range crashes {
			if now.Sub(t) < crashWindow {
				recent = append(recent, t)
			}
		}
		crashes = append(recent, now)
		if len(crashes) > len(restartDelays) {
			title := fmt.Sprintf("dsh 在 %.0f 分钟内连续崩溃 %d 次，已停止自动重启", crashWindow.Minutes(), len(crashes))
			var ok bool
			if note, ok = waitRetry(ctx, win, retry, title, crashErr, p.Tail(), logPath); !ok {
				return
			}
			crashes = nil
			continue
		}

		delay := restartDelays[len(crashes)-1]
		note = fmt.Sprintf("dsh 意外退出（%v），自动重启（第 %d 次）", crashErr, len(crashes))
		log.Printf("%s，%v 后开始", note, delay)
		win.Page().LoadHTML(loadingPage(0, time.Now(), note, ""), "")
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// waitRetry 显示报错页，等用户点「重试」或在托盘里重启/换 profile。
// 返回下次加载页的说明；窗口关闭时 ok 为 false。
func waitRetry(ctx context.Context, win *mygo.Window, retry <-chan string, title string, err error, tail, logPath string) (note string, ok bool) {
	log.Printf("出错：%s: %v", title, err)
	setDshDown(true)
	// 清掉报错页出现之前的残留点击
	select {
	case <-retry:
	default:
	}
	win.Page().LoadHTML(errorPage(title, err, tail, logPath), "")
	select {
	case <-ctx.Done():
		return "", false
	case msg := <-retry:
		log.Printf("重试 %s", msg)
		return msg, true
	}
}

// bootOnce 拉起一个 dsh 并把窗口切到它的 Web UI。port 为 0 时自动找；
// 返回实际用的端口。出错时 dsh 已被回收，返回的 p 只用来取输出尾部。
func bootOnce(ctx context.Context, win *mygo.Window, port int, note string) (p *dshProc, usedPort int, err error) {
	started := time.Now()
	resetStates.Store(true) // 新的 dsh 进程：上一个被打断的轮次不会再写 turn/end
	profile := activeProfile()
	meta := "profile " + profile
	win.Page().LoadHTML(loadingPage(0, started, note, meta), "")
	defer func() {
		if err != nil && p != nil {
			p.Stop() // 先回收，别留孤儿占端口和 profile 锁
		}
	}()

	bin, err := findDsh(*dshBin)
	if err != nil {
		return nil, 0, &bootError{"找不到 dsh", err}
	}
	log.Printf("dsh: %s", bin)

	// 版本不对时常见的症状是卡在「等登录地址」90 秒，先查一下能早点给出明确原因
	verWarn := ""
	if !*skipVer {
		vc := checkDshVersion(ctx, bin)
		log.Printf("dsh 版本: %s", vc.version)
		if vc.tooOld {
			return nil, 0, &bootError{"dsh 版本过低", fmt.Errorf("当前 %s，至少需要 %s。\n%s", vc.version, minDshVersion, upgradeHint)}
		}
		if vc.warn != "" {
			log.Printf("版本提示：%s", vc.warn)
			verWarn = vc.warn
		}
		if vc.version != "" {
			meta += " · dsh " + vc.version
		}
	}
	if verWarn != "" {
		note = strings.TrimPrefix(note+"；"+verWarn, "；")
	}
	// 卡在启动阶段时把版本提示附在报错后面
	withVerHint := func(err error) error {
		if verWarn == "" {
			return err
		}
		return fmt.Errorf("%w\n\n%s", err, verWarn)
	}

	if *portArg == 0 {
		start := port
		if start == 0 {
			start = 18200
		}
		if port, err = freePort(start); err != nil {
			return nil, 0, &bootError{"找不到可用端口", err}
		}
	}
	log.Printf("端口: %d", port)

	log.Printf("profile: %s", profile)
	if p, err = startDsh(bin, port, profile); err != nil {
		return nil, port, &bootError{"dsh 启动失败", err}
	}
	current.Lock()
	quitting := current.quitting
	if !quitting {
		current.proc = p
	}
	current.Unlock()
	if quitting {
		return p, port, context.Canceled
	}

	win.Page().LoadHTML(loadingPage(1, started, note, meta), "")
	authURL, err := p.WaitToken(ctx, 90*time.Second)
	if err != nil {
		return p, port, &bootError{"等不到 dsh 的登录地址", withVerHint(err)}
	}
	log.Printf("拿到地址: %s", maskToken(authURL))

	win.Page().LoadHTML(loadingPage(2, started, note, meta), "")
	if err := p.WaitReady(ctx, authURL, 90*time.Second); err != nil {
		return p, port, &bootError{"dsh 后端没就绪", withVerHint(err)}
	}
	log.Printf("后端就绪（%.1fs），加载界面", time.Since(started).Seconds())
	setDshDown(false)
	refreshTray() // 顺带刷新托盘里的 profile 列表
	if err := win.Page().LoadURL(authURL); err != nil {
		return p, port, &bootError{"加载界面失败", err}
	}
	return p, port, nil
}

// ---- 环境修复 ----

func ensureNoProxyForLoopback() {
	loopback := "127.0.0.1,localhost,::1,0.0.0.0"
	for _, k := range []string{"NO_PROXY", "no_proxy"} {
		cur := os.Getenv(k)
		if cur == "" {
			_ = os.Setenv(k, loopback)
			continue
		}
		if !strings.Contains(cur, "127.0.0.1") {
			_ = os.Setenv(k, cur+","+loopback) // 追加，别覆盖用户自己的白名单
		}
	}
}

// ---- 杂项 ----

func setupLog() string {
	logDir := filepath.Join(homeDir(), ".dsh-desktop", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, "app.log")
	log.SetFlags(log.Ltime)
	if lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		log.SetOutput(io.MultiWriter(os.Stderr, lf)) // 进程退出时由系统关闭
	} else {
		fmt.Fprintf(os.Stderr, "日志文件打不开（%v），只输出到终端\n", err)
	}
	return logPath
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}
