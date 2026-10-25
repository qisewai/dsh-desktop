package main

import (
	"fmt"
	"image/color"
	"strings"
	"sync"

	"dshdesktop/internal/appicon"
)

// 托盘图标反映运行状态：右下角圆点，优先级 红（dsh 没在正常运行）> 橙（等你审批/回答）
// > 绿（有会话在跑）> 无（空闲）。会话状态来自 notify.go 读的会话事件日志。

type sessionCounts struct {
	running, approvals, questions int
}

var status struct {
	sync.Mutex
	counts  sessionCounts
	dshDown bool // supervise 正在显示报错页
	shown   string
}

func setSessionCounts(c sessionCounts) {
	status.Lock()
	status.counts = c
	status.Unlock()
	publishStatus()
}

func setDshDown(down bool) {
	status.Lock()
	status.dshDown = down
	status.Unlock()
	publishStatus()
}

// statusText 是托盘菜单顶上那行和悬停提示里的状态，空串表示空闲。
func statusText() string {
	status.Lock()
	defer status.Unlock()
	if status.dshDown {
		return "● dsh 没在运行，打开窗口查看"
	}
	c := status.counts
	var parts []string
	if c.approvals > 0 {
		parts = append(parts, fmt.Sprintf("%d 个等你审批", c.approvals))
	}
	if c.questions > 0 {
		parts = append(parts, fmt.Sprintf("%d 个等你回答", c.questions))
	}
	if c.running > 0 {
		parts = append(parts, fmt.Sprintf("%d 个会话运行中", c.running))
	}
	if len(parts) == 0 {
		return ""
	}
	return "● " + strings.Join(parts, " · ")
}

func statusBadge() (color.NRGBA, bool) {
	status.Lock()
	defer status.Unlock()
	switch c := status.counts; {
	case status.dshDown:
		return appicon.BadgeError, true
	case c.approvals+c.questions > 0:
		return appicon.BadgeAttention, true
	case c.running > 0:
		return appicon.BadgeRunning, true
	}
	return color.NRGBA{}, false
}

var (
	iconCacheMu sync.Mutex
	iconCache   = map[color.NRGBA][]byte{}
)

// trayIcon 返回当前状态的托盘图标。几种图标各画一次缓存起来。
func trayIcon() []byte {
	badge, ok := statusBadge()
	iconCacheMu.Lock()
	defer iconCacheMu.Unlock()
	if b, hit := iconCache[badge]; hit {
		return b
	}
	b := appicon.PNG(64)
	if ok {
		b = appicon.Badged(64, badge)
	}
	iconCache[badge] = b
	return b
}

// publishStatus 状态有变化时更新托盘：图标、悬停提示、菜单顶上的状态行。
// 没变就什么都不做——Linux 的 AppIndicator 每换一次图标都要写一个新文件，
// 而且重建菜单会把用户正打开着的菜单关掉。
func publishStatus() {
	badge, ok := statusBadge()
	key := fmt.Sprintf("%v|%v|%s", ok, badge, statusText())
	status.Lock()
	changed := key != status.shown
	status.shown = key
	status.Unlock()
	if changed && appTray != nil {
		_ = appTray.SetIcon(trayIcon(), false)
		refreshTray()
	}
}
