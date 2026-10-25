package main

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// dsh 版本兼容：壳依赖 dsh 的几处外部行为——`--profile <name>` 写法、web 应用的
// --port/--no-open、stdout 打印的 `http://127.0.0.1:<port>/?token=…`、带 token 访问时
// 303 + Set-Cookie。这些一变，壳就会卡在「等登录地址」或「等就绪」。
const (
	// minDshVersion 以下直接拦住。取自 DSH Tauri 桌面版声明的最低内核版本，本壳没在更老的版本上验证过。
	minDshVersion = "0.1.7-rc.1"
	// testedDshVersion 是实际验证过的版本；比它的 主.次 版本新就提示一句，照常启动。
	testedDshVersion = "0.2.1-rc.2"
)

const upgradeHint = "升级：npm i -g @deepseek-ai/dsh@latest（Linux 上全局装在 /usr 下的话要 sudo），升级后点「重试」。\n" +
	"确定要跳过检查可加 -skip-version-check 启动。"

// versionCheck 是一次检查的结论。tooOld 时不该启动；warn 非空时照常启动但要告诉用户。
type versionCheck struct {
	version string
	tooOld  bool
	warn    string
}

func checkDshVersion(ctx context.Context, bin string) versionCheck {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	hideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return versionCheck{warn: fmt.Sprintf("读不到 dsh 版本（%v），跳过兼容性检查", err)}
	}
	v := strings.TrimSpace(string(out))
	if i := strings.LastIndexByte(v, '\n'); i >= 0 {
		v = strings.TrimSpace(v[i+1:]) // 只取最后一行，前面可能有告警
	}
	v = strings.TrimPrefix(v, "v")
	c := versionCheck{version: v}
	if _, ok := parseVersion(v); !ok {
		c.warn = fmt.Sprintf("看不懂 dsh 版本号 %q，跳过兼容性检查", v)
		return c
	}
	switch {
	case compareVersions(v, minDshVersion) < 0:
		c.tooOld = true
	case newerMinor(v, testedDshVersion):
		c.warn = fmt.Sprintf("dsh %s 比验证过的 %s 新，如果卡在启动阶段多半是 dsh 改了输出格式", v, testedDshVersion)
	}
	return c
}

// semver 的子集：主.次.修订，可带 -预发布 段，忽略 +构建 段。
type version struct {
	nums [3]int
	pre  []string
}

func parseVersion(s string) (version, bool) {
	var v version
	s, _, _ = strings.Cut(s, "+")
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v.nums[i] = n
	}
	if hasPre {
		if pre == "" {
			return v, false
		}
		v.pre = strings.Split(pre, ".")
	}
	return v, true
}

// compareVersions 按 semver 规则比较，解析不了的一律当相等。
func compareVersions(a, b string) int {
	va, ok1 := parseVersion(a)
	vb, ok2 := parseVersion(b)
	if !ok1 || !ok2 {
		return 0
	}
	for i := range va.nums {
		if va.nums[i] != vb.nums[i] {
			return cmpInt(va.nums[i], vb.nums[i])
		}
	}
	// 有预发布段的比正式版小：1.0.0-rc.1 < 1.0.0
	switch {
	case len(va.pre) == 0 && len(vb.pre) == 0:
		return 0
	case len(va.pre) == 0:
		return 1
	case len(vb.pre) == 0:
		return -1
	}
	for i := 0; i < len(va.pre) && i < len(vb.pre); i++ {
		x, y := va.pre[i], vb.pre[i]
		nx, errX := strconv.Atoi(x)
		ny, errY := strconv.Atoi(y)
		switch {
		case errX == nil && errY == nil:
			if nx != ny {
				return cmpInt(nx, ny)
			}
		case errX == nil: // 数字段比字母段小
			return -1
		case errY == nil:
			return 1
		default:
			if c := strings.Compare(x, y); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(va.pre), len(vb.pre))
}

// newerMinor 报告 a 的 主.次 是否比 b 的新（修订号、预发布的差别不算）。
func newerMinor(a, b string) bool {
	va, ok1 := parseVersion(a)
	vb, ok2 := parseVersion(b)
	if !ok1 || !ok2 {
		return false
	}
	if va.nums[0] != vb.nums[0] {
		return va.nums[0] > vb.nums[0]
	}
	return va.nums[1] > vb.nums[1]
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
