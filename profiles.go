package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// profile：dsh 的 profile 就是 $DSH_HOME/profiles 下的一个目录。壳只能装能出 Web UI 的 profile
// （bundles 里有 dsh-web-app），tui/headless 之类起来也没有 HTTP 服务可装。
//
// 注意启动写法必须是 `dsh --profile <name> --port … --no-open`：
// `dsh web … --profile x` 里的 --profile 会被当成 web 应用自己的参数，dsh 直接报
// unknown option '--profile'。

const (
	defaultProfile = "web"                      // dsh 自带、首次使用时自动初始化
	webAppBundle   = "@deepseek-ai/dsh-web-app" // 有它才有 Web UI
)

func dshHome() string {
	if h := os.Getenv("DSH_HOME"); h != "" {
		return h
	}
	return filepath.Join(homeDir(), ".dsh")
}

// listWebProfiles 列出能装进窗口的 profile，按名字排序，web 总在里面。
func listWebProfiles() []string {
	names := []string{defaultProfile}
	entries, err := os.ReadDir(filepath.Join(dshHome(), "profiles"))
	if err != nil {
		return names
	}
	for _, e := range entries {
		n := e.Name()
		// desktop 归 Electron 版管，dsh 会拒绝；点开头的是隐藏目录
		if !e.IsDir() || n == defaultProfile || n == "desktop" || strings.HasPrefix(n, ".") {
			continue
		}
		if isWebProfile(n) {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	return names
}

func isWebProfile(name string) bool {
	b, err := os.ReadFile(filepath.Join(dshHome(), "profiles", name, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Dsh struct {
			Profile struct {
				Bundles []string `json:"bundles"`
			} `json:"profile"`
		} `json:"dsh"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return false
	}
	return slices.Contains(pkg.Dsh.Profile.Bundles, webAppBundle)
}

// ---- 当前 profile 与持久化 ----

// 托盘里选的 profile 存在这里，下次启动沿用；命令行 -profile 优先，但不写回。
type settings struct {
	Profile string `json:"profile,omitempty"`
	// 指针区分「没设过」（用默认值 true）和「明确关掉」
	Notify    *bool `json:"notify,omitempty"`    // 轮次完成 / 审批 / 提问的系统通知
	URLScheme *bool `json:"urlScheme,omitempty"` // 接管 dsh:// 链接
}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// updateSettings 读-改-写 settings.json。
func updateSettings(fn func(*settings)) {
	s := loadSettings()
	fn(&s)
	saveSettings(s)
}

func settingsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(homeDir(), ".config")
	}
	return filepath.Join(dir, "dsh-desktop", "settings.json")
}

func loadSettings() settings {
	var s settings
	if b, err := os.ReadFile(settingsPath()); err == nil {
		if err := json.Unmarshal(b, &s); err != nil {
			log.Printf("设置文件损坏，忽略：%v", err)
		}
	}
	return s
}

func saveSettings(s settings) {
	p := settingsPath()
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	b, _ := json.MarshalIndent(s, "", "  ")
	// 先写临时文件再改名，写一半崩了也不会留下半个 JSON
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err == nil {
		err = os.Rename(tmp, p)
		if err == nil {
			return
		}
	}
	log.Printf("保存设置失败：%s", p)
}

var active struct {
	sync.Mutex
	profile string
}

// initProfile 决定启动用哪个 profile：-profile > 上次在托盘选的 > web。
func initProfile(flagValue string) {
	p := flagValue
	if p == "" {
		p = loadSettings().Profile
		if p != "" && !slices.Contains(listWebProfiles(), p) {
			log.Printf("上次用的 profile %q 已不存在或不是 Web profile，改用 %s", p, defaultProfile)
			p = ""
		}
	}
	if p == "" {
		p = defaultProfile
	}
	setActiveProfile(p, false)
}

func activeProfile() string {
	active.Lock()
	defer active.Unlock()
	return active.profile
}

func setActiveProfile(name string, persist bool) {
	active.Lock()
	active.profile = name
	active.Unlock()
	if persist {
		updateSettings(func(s *settings) { s.Profile = name })
	}
}
