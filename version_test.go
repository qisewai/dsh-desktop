package main

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.2.1-rc.2", "0.2.1-rc.2", 0},
		{"0.2.1-rc.2", "0.2.1-rc.10", -1}, // 数字段按数值比，不是字典序
		{"0.2.1-rc.2", "0.2.1", -1},       // 预发布 < 正式版
		{"0.2.1", "0.2.0", 1},
		{"0.1.7-rc.1", "0.1.7-beta.3", 1}, // rc > beta（字母序）
		{"0.1.7-rc", "0.1.7-rc.1", -1},    // 段少的小
		{"0.1.7-1", "0.1.7-rc", -1},       // 数字段 < 字母段
		{"1.0.0+build.5", "1.0.0", 0},     // 构建元数据不参与比较
		{"0.10.0", "0.9.9", 1},
		{"garbage", "0.1.0", 0}, // 解析不了当相等
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNewerMinor(t *testing.T) {
	cases := []struct {
		a    string
		want bool
	}{
		{"0.2.1-rc.2", false},
		{"0.2.9", false}, // 修订号新不算
		{"0.3.0-rc.1", true},
		{"1.0.0", true},
		{"0.1.9", false},
	}
	for _, c := range cases {
		if got := newerMinor(c.a, testedDshVersion); got != c.want {
			t.Errorf("newerMinor(%q) = %v, want %v", c.a, got, c.want)
		}
	}
}

func TestParseVersionRejects(t *testing.T) {
	for _, s := range []string{"", "1.2", "1.2.3.4", "1.2.x", "1.2.3-", "-1.0.0"} {
		if _, ok := parseVersion(s); ok {
			t.Errorf("parseVersion(%q) should fail", s)
		}
	}
}
