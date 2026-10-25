package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// 事件结构照 dsh 0.2.1-rc.2 实际落盘的会话日志写，每次 appendFrame 模拟 dsh 一次提交（一个 zstd 帧）。

type sent struct{ id, title, body string }

func newTestNotifier(t *testing.T) (*notifier, *[]sent) {
	t.Helper()
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dec.Close)
	var got []sent
	notifyEnabled.Store(true)
	n := &notifier{root: t.TempDir(), dec: dec, logs: map[string]*sessionLog{},
		send: func(id, title, body string) { got = append(got, sent{id, title, body}) }}
	return n, &got
}

func sessionFile(t *testing.T, n *notifier, id string) string {
	t.Helper()
	dir := filepath.Join(n.root, "--ws--", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, sessionLogName)
}

func appendFrame(t *testing.T, path string, events ...any) {
	t.Helper()
	var plain []byte
	for _, e := range events {
		b, _ := json.Marshal(e)
		plain = append(append(plain, b...), '\n')
	}
	enc, _ := zstd.NewWriter(nil)
	frame := enc.EncodeAll(plain, nil)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(frame); err != nil {
		t.Fatal(err)
	}
}

func ev(typ string, data any) map[string]any {
	return map[string]any{"type": typ, "seq": 1, "time": 1, "data": data}
}

func header(depth int) map[string]any {
	return map[string]any{"type": "session", "version": 4, "id": "x", "delegationDepth": depth}
}

func assistant(text string) map[string]any {
	return ev("assistant/message", map[string]any{"message": map[string]any{"role": "assistant",
		"content": []any{map[string]any{"type": "reasoning", "text": "想一想"}, map[string]any{"type": "text", "text": text}}}})
}

func turnEnd(kind string) map[string]any {
	return ev("turn/end", map[string]any{"turn": 1, "reason": map[string]any{"kind": kind}})
}

func TestNotifierExistingSession(t *testing.T) {
	n, got := newTestNotifier(t)
	p := sessionFile(t, n, "session-old")
	appendFrame(t, p, header(0), ev("session/title", map[string]any{"title": "旧会话"}))
	appendFrame(t, p, assistant("早就完成的回复"), turnEnd("completed"))
	n.scan(true) // 启动前的内容：不提醒
	if len(*got) != 0 {
		t.Fatalf("baseline should not notify: %+v", *got)
	}

	appendFrame(t, p, ev("turn/start", map[string]any{"turn": 2}), assistant("新的回复\n第二行"))
	appendFrame(t, p, turnEnd("completed"))
	n.scan(false)
	if len(*got) != 1 {
		t.Fatalf("want 1 notification, got %+v", *got)
	}
	g := (*got)[0]
	if g.title != "✅ 已完成 · 旧会话" || g.body != "新的回复 第二行" || g.id != "dsh-session-old-turn" {
		t.Errorf("unexpected %+v", g)
	}
}

func TestNotifierApprovalQuestionCancel(t *testing.T) {
	n, got := newTestNotifier(t)
	n.scan(true)
	p := sessionFile(t, n, "session-new") // 启动后才出现的会话：从头读
	appendFrame(t, p, header(0), ev("session/title", map[string]any{"title": "新会话"}))
	appendFrame(t, p, ev("approval/asked", map[string]any{"id": "a1", "toolName": "bash",
		"reason": "escalate sandbox to danger-full-access: 该命令需要在工作区外创建文件 ~/x.txt，当前沙箱不允许。"}))
	appendFrame(t, p, ev("tool/call", map[string]any{"name": "ask_user_question",
		"arguments": `{"questions":[{"id":"q","header":"选择","question":"选 A 还是 B？"},{"id":"q2","question":"再问一个"}]}`}))
	appendFrame(t, p, ev("tool/call", map[string]any{"name": "bash", "arguments": `{"command":"ls"}`}))
	appendFrame(t, p, assistant("被打断了"), turnEnd("cancelled"))
	n.scan(false)

	want := []sent{
		{"dsh-session-new-approval", "🔐 等你审批 · 新会话", "bash：该命令需要在工作区外创建文件 ~/x.txt"},
		{"dsh-session-new-question", "❓ 等你回答 · 新会话", "选 A 还是 B？（共 2 个问题）"},
	}
	if len(*got) != len(want) {
		t.Fatalf("want %d notifications, got %+v", len(want), *got)
	}
	for i := range want {
		if (*got)[i] != want[i] {
			t.Errorf("#%d = %+v, want %+v", i, (*got)[i], want[i])
		}
	}
}

func TestNotifierSkipsSubagentAndDisabled(t *testing.T) {
	n, got := newTestNotifier(t)
	n.scan(true)
	sub := sessionFile(t, n, "session-sub")
	appendFrame(t, sub, header(1), assistant("子代理干完了"), turnEnd("completed"))
	n.scan(false)
	if len(*got) != 0 {
		t.Fatalf("subagent should not notify: %+v", *got)
	}

	notifyEnabled.Store(false)
	p := sessionFile(t, n, "session-quiet")
	appendFrame(t, p, header(0), assistant("完成"), turnEnd("completed"))
	n.scan(false)
	if len(*got) != 0 {
		t.Fatalf("disabled should not notify: %+v", *got)
	}
}

func TestNotifierPartialFrame(t *testing.T) {
	n, got := newTestNotifier(t)
	n.scan(true)
	p := sessionFile(t, n, "session-partial")
	appendFrame(t, p, header(0))
	n.scan(false)

	// 把一帧拆成两半写：前半段解不开，不能丢也不能报错；后半段到了再一起解
	var plain []byte
	for _, e := range []any{assistant("好了"), turnEnd("completed")} {
		b, _ := json.Marshal(e)
		plain = append(append(plain, b...), '\n')
	}
	enc, _ := zstd.NewWriter(nil)
	frame := enc.EncodeAll(plain, nil)
	half := len(frame) / 2
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	f.Write(frame[:half])
	n.scan(false)
	if len(*got) != 0 {
		t.Fatalf("half frame should not notify yet: %+v", *got)
	}
	f.Write(frame[half:])
	f.Close()
	n.scan(false)
	if len(*got) != 1 || !strings.HasPrefix((*got)[0].title, "✅ 已完成") || (*got)[0].body != "好了" {
		t.Fatalf("after full frame: %+v", *got)
	}
}

func TestApprovalBodyAndOpenURL(t *testing.T) {
	cases := map[[2]string]string{
		{"bash", "escalate sandbox to danger-full-access: 需要写文件，因为……"}: "bash：需要写文件",
		{"edit", "修改 /etc/hosts"}: "edit：修改 /etc/hosts",
		{"bash", ""}:              "bash 需要授权",
		{"", ""}:                  "有操作需要授权",
	}
	for in, want := range cases {
		if got := approvalBody(in[0], in[1]); got != want {
			t.Errorf("approvalBody(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
	for u, want := range map[string]bool{"dsh://open": true, "DSH://open/": true, "dsh://oauth/callback": false, "dsh://open?x=1": false} {
		if isOpenURL(u) != want {
			t.Errorf("isOpenURL(%q) != %v", u, want)
		}
	}
}

func TestNotifierCounts(t *testing.T) {
	n, _ := newTestNotifier(t)
	var last sessionCounts
	n.onCounts = func(c sessionCounts) { last = c }
	n.scan(true)

	a := sessionFile(t, n, "session-a")
	b := sessionFile(t, n, "session-b")
	sub := sessionFile(t, n, "session-sub")
	appendFrame(t, a, header(0), ev("turn/start", map[string]any{"turn": 1}))
	appendFrame(t, b, header(0), ev("turn/start", map[string]any{"turn": 1}),
		ev("approval/asked", map[string]any{"id": "ap1", "toolName": "bash", "reason": "x"}))
	appendFrame(t, sub, header(1), ev("turn/start", map[string]any{"turn": 1}))
	n.scan(false)
	if want := (sessionCounts{running: 1, approvals: 1}); last != want {
		t.Fatalf("after start: %+v, want %+v", last, want)
	}

	// 审批决定后回到运行中；a 进入提问
	appendFrame(t, b, ev("approval/decided", map[string]any{"id": "ap1", "outcome": "approved"}))
	appendFrame(t, a, ev("tool/call", map[string]any{"name": "ask_user_question", "callId": "q1", "arguments": `{"questions":[{"question":"?"}]}`}))
	n.scan(false)
	if want := (sessionCounts{running: 1, questions: 1}); last != want {
		t.Fatalf("after decide/ask: %+v, want %+v", last, want)
	}

	// 回答完、两边都结束
	appendFrame(t, a, ev("tool/result", map[string]any{"message": map[string]any{"toolCallId": "q1"}}), turnEnd("completed"))
	appendFrame(t, b, turnEnd("completed"))
	n.scan(false)
	if last != (sessionCounts{}) {
		t.Fatalf("after end: %+v, want idle", last)
	}

	// dsh 重启：被打断的轮次没有 turn/end，也要清零
	appendFrame(t, a, ev("turn/start", map[string]any{"turn": 2}))
	n.scan(false)
	if last.running != 1 {
		t.Fatalf("running again: %+v", last)
	}
	resetStates.Store(true)
	n.scan(false)
	if last != (sessionCounts{}) {
		t.Fatalf("after reset: %+v, want idle", last)
	}
}

func TestNotifierStaleRunning(t *testing.T) {
	n, _ := newTestNotifier(t)
	n.scan(true)
	a := sessionFile(t, n, "session-stale")
	appendFrame(t, a, header(0), ev("turn/start", map[string]any{"turn": 1}))
	n.scan(false)
	l := n.logs[a]
	if c := n.counts(l.modified.Add(time.Minute)); c.running != 1 {
		t.Fatalf("fresh: %+v", c)
	}
	if c := n.counts(l.modified.Add(staleRunning + time.Minute)); c.running != 0 {
		t.Fatalf("stale: %+v", c)
	}
	// 等审批的不过期
	appendFrame(t, a, ev("turn/start", map[string]any{"turn": 2}), ev("approval/asked", map[string]any{"id": "x"}))
	n.scan(false)
	if c := n.counts(l.modified.Add(5 * staleRunning)); c.approvals != 1 || !l.running {
		t.Fatalf("waiting should not expire: %+v", c)
	}
}
