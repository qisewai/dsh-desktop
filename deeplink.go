package main

import (
	"log"
	"strings"

	"github.com/egoist/mygo"
)

// dsh:// 深链接：和官方 Electron 版、DSH Tauri 用同一个 scheme，只认 dsh://open（把主窗口调出来）。
// DeepSeek 平台登录成功页的「打开应用」按钮落在这里；dsh://oauth/callback 等其他地址
// 官方桌面版也不处理，这里同样忽略。
//
// Linux 上注册 = 在 ~/.local/share/applications 写一个隐藏的 dsh-desktop.url-handler.desktop，
// 并在 mimeapps.list 里设为 x-scheme-handler/dsh 的默认处理程序（等同 xdg-mime default）。
// 托盘里可以关掉，关掉会撤销注册。

const urlScheme = "dsh"

// setupURLScheme 按设置注册，并接上 OnOpenURL。须在 App.Run 之前调用：
// 冷启动带的 dsh://open 参数要在注册之后才会被 MyGo 认出来。
//
// 只有打包安装的版本才自动注册：开发版（./启动.sh）和安装版要是都注册，谁最后启动
// 默认处理程序就归谁，来回抢。开发版需要时在托盘里手动勾选。
func setupURLScheme(show func()) {
	if mygo.App.IsPackaged() && boolOr(loadSettings().URLScheme, true) {
		if err := mygo.App.RegisterURLScheme(urlScheme); err != nil {
			log.Printf("注册 dsh:// 失败：%v", err)
		}
	}
	mygo.App.OnOpenURL(func(u string) {
		if isOpenURL(u) {
			log.Printf("深链接 %s：显示窗口", u)
			show()
			return
		}
		log.Printf("深链接 %s：不认识，忽略", u)
	})
}

func isOpenURL(u string) bool {
	u = strings.ToLower(strings.TrimSpace(u))
	return u == urlScheme+"://open" || u == urlScheme+"://open/"
}

// setURLSchemeEnabled 是托盘开关的动作。
func setURLSchemeEnabled(on bool) {
	var err error
	if on {
		err = mygo.App.RegisterURLScheme(urlScheme)
	} else {
		err = mygo.App.UnregisterURLScheme(urlScheme)
	}
	if err != nil {
		log.Printf("切换 dsh:// 注册失败：%v", err)
		return
	}
	updateSettings(func(s *settings) { s.URLScheme = &on })
	log.Printf("dsh:// 链接处理：%v", on)
}
