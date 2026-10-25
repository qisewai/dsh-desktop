package main

import (
	"log"
	"sync"

	"github.com/egoist/mygo"
)

// 托盘：Linux 下走 AppIndicator（StatusNotifierItem），需要 libayatana-appindicator3。
// AppIndicator 不给单击事件，点图标只弹菜单，所以「显示窗口」做成菜单第一项。

// appTray 必须一直被引用着：MyGo 的菜单注册表只存菜单项的弱引用，Linux 托盘后端也不持有
// Go 侧的 *Tray。setupTray 返回后要是没人引用它，GC 一跑菜单项就被回收，之后点菜单
// 静默无反应（刚启动时点却正常，极难复现）。
var appTray *mygo.Tray

// setupTray 建托盘图标；失败（没装 appindicator、桌面不支持）时返回 false，
// 调用方就别把关窗口改成隐藏，否则窗口藏起来就找不回来了。
// restart 让 supervise 重启 dsh，参数是加载页上的说明。
func setupTray(win *mygo.Window, restart func(note string)) bool {
	trayCtl.win, trayCtl.restart = win, restart
	t, err := mygo.NewTray(mygo.TrayOptions{
		ID:      "dsh-desktop",
		Icon:    trayIcon(),
		ToolTip: trayToolTip(),
		Menu:    buildTrayMenu(),
	})
	if err != nil {
		log.Printf("托盘不可用，关窗口即退出：%v", err)
		return false
	}
	t.OnClick(func() { showWindow(win) }) // macOS / Windows 有单击事件，Linux 不会触发
	appTray = t
	return true
}

var trayCtl struct {
	win     *mygo.Window
	restart func(note string)
}

// refreshTray 重建托盘菜单：profile 列表在每次 dsh 启动成功后刷新一次，
// AppIndicator 没有「菜单将要打开」的回调，没法每次点开时现查。任意 goroutine 可调。
func refreshTray() {
	if appTray == nil {
		return
	}
	appTray.SetMenu(buildTrayMenu())
	appTray.SetToolTip(trayToolTip())
}

func trayToolTip() string {
	tip := "DeepSeek Harness · " + activeProfile()
	if st := statusText(); st != "" {
		tip += "\n" + st
	}
	return tip
}

func buildTrayMenu() *mygo.Menu {
	win, restart := trayCtl.win, trayCtl.restart
	cur := activeProfile()
	var profileItems []*mygo.MenuItem
	for _, name := range listWebProfiles() {
		profileItems = append(profileItems, &mygo.MenuItem{
			Label:   name,
			Type:    mygo.MenuItemRadio,
			Checked: name == cur,
			Click: func(*mygo.MenuItem, *mygo.Window) {
				if name == activeProfile() {
					return
				}
				log.Printf("切换 profile：%s → %s", activeProfile(), name)
				setActiveProfile(name, true)
				refreshTray()
				showWindow(win) // 切换时让用户看到加载进度
				restart("切换到 profile " + name)
			},
		})
	}
	var top []*mygo.MenuItem
	if st := statusText(); st != "" {
		// 点它也调出窗口：要审批、要回答都得去窗口里
		top = append(top, &mygo.MenuItem{Label: st, Click: func(*mygo.MenuItem, *mygo.Window) { showWindow(win) }}, mygo.Separator())
	}
	return mygo.NewMenu(append(top, []*mygo.MenuItem{
		{Label: "显示窗口", Click: func(*mygo.MenuItem, *mygo.Window) { showWindow(win) }},
		{Label: "隐藏窗口", Click: func(*mygo.MenuItem, *mygo.Window) { win.Hide() }},
		mygo.Separator(),
		{Label: "Profile：" + cur, Type: mygo.MenuItemSubmenu, Submenu: profileItems},
		{Label: "重启 dsh", Click: func(*mygo.MenuItem, *mygo.Window) { restart("手动重启") }},
		mygo.Separator(),
		{
			Label: "完成 / 审批 / 提问时通知", Type: mygo.MenuItemCheckbox, Checked: notifyEnabled.Load(),
			Click: func(it *mygo.MenuItem, _ *mygo.Window) {
				on := it.IsChecked() // MyGo 在 Click 之前已经翻转过
				notifyEnabled.Store(on)
				updateSettings(func(s *settings) { s.Notify = &on })
				log.Printf("系统通知：%v", on)
			},
		},
		{
			Label: "处理 dsh:// 链接", Type: mygo.MenuItemCheckbox, Checked: mygo.App.IsURLSchemeRegistered(urlScheme),
			Click: func(it *mygo.MenuItem, _ *mygo.Window) {
				setURLSchemeEnabled(it.IsChecked())
				refreshTray() // 注册失败时把勾选状态改回真实情况
			},
		},
		mygo.Separator(),
		{Label: "退出", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	}...))
}

func showWindow(win *mygo.Window) {
	if win == nil || win.IsDestroyed() {
		return
	}
	if win.IsMinimized() {
		win.Restore()
	}
	win.Show()
	win.Focus()
}

var hideNoticeOnce sync.Once

// noticeHiddenToTray 第一次把窗口藏进托盘时提示一下，不然会以为程序已经退了还在后台跑。
func noticeHiddenToTray() {
	hideNoticeOnce.Do(func() {
		n := mygo.NewNotification(mygo.NotificationOptions{
			ID:     "dsh-desktop-hidden",
			Title:  "DeepSeek Harness 仍在后台运行",
			Body:   "窗口已收进托盘。要彻底退出，请在托盘菜单里选「退出」。",
			Silent: true,
		})
		if err := n.Show(); err != nil {
			log.Printf("通知发送失败：%v", err)
		}
	})
}
