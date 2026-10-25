package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/klauspost/compress/zstd"
)

// 系统通知：轮次完成、等待审批、等待回答。
//
// 数据来源是 dsh 落盘的会话事件日志 $DSH_HOME/sessions/<工作区>/<会话>/session.v4.jsonl.zstd，
// 不是 Web UI。dsh 前端的会话状态只在它自己的 Cordis 上下文里，壳碰不到；/api/session/list
// 能看到 running，却看不到待审批。事件日志三样都有，而且：
//   - 每次提交追加一个独立的 zstd 帧，可以记住偏移量只解新增部分；
//   - 每行一个 {type, seq, time, data}，首行是 {type:"session", delegationDepth, …} 头。
// 文件名里的 v4 是 dsh 的持久化格式版本，哪天换了格式这里就找不到文件，通知静默失效，不会误报。
//
// 用到的事件（dsh 0.2.1-rc.2 实测）：
//   session/title     {title}
//   assistant/message {message:{content:[{type:"text",text}…]}}
//   tool/call         {name:"ask_user_question", arguments:"{\"questions\":[{question,header}…]}"}
//   approval/asked    {id, toolName, reason}
//   turn/end          {reason:{kind:"completed"|…}}

const (
	sessionLogName  = "session.v4.jsonl.zstd"
	notifyScanEvery = 1500 * time.Millisecond
	maxNotifyBody   = 200
	// 运行中的会话这么久没有新事件就当它停了：dsh 被杀时被打断的轮次不会写 turn/end。
	// 等你审批/回答的不过期，人可能离开很久。
	staleRunning = 30 * time.Minute
)

var (
	notifyEnabled atomic.Bool
	// resetStates 让 notifier 下一轮把所有会话当成空闲：dsh 重启时被打断的轮次永远等不到 turn/end。
	resetStates atomic.Bool
)

// sessionLog 是对一个会话日志文件的跟踪状态。
type sessionLog struct {
	id      string
	offset  int64  // 已解码到的字节位置（帧边界）
	partial []byte // 上次解出来、还没换行的半行
	stuck   int    // 新增字节连续解不开的次数
	loaded  bool   // 头部和标题是否已读过（启动时已有的文件延后到第一次需要时再读）
	depth   int    // delegationDepth，>0 是子代理会话
	title   string
	reply   string // 最近一条助手正文

	// 托盘状态用
	running  bool
	pending  map[string]string // 审批 id / 提问的 callId → "approval" | "question"
	modified time.Time         // 日志文件最后修改时间
}

type notifier struct {
	root string
	dec  *zstd.Decoder
	logs map[string]*sessionLog
	win  *mygo.Window
	send func(id, title, body string) // 测试里替换掉，默认发系统通知
	// onCounts 每轮扫描后收到会话状态汇总（托盘用），可以为 nil
	onCounts func(sessionCounts)
}

// startNotifier 在后台盯着会话日志发通知，直到 stop 关闭。
func startNotifier(win *mygo.Window, stop <-chan struct{}) {
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		log.Printf("通知不可用：%v", err)
		return
	}
	n := &notifier{root: filepath.Join(dshHome(), "sessions"), dec: dec, logs: map[string]*sessionLog{}, win: win,
		send: showNotification, onCounts: setSessionCounts}
	n.scan(true) // 启动前就有的内容不提醒
	go func() {
		defer dec.Close()
		t := time.NewTicker(notifyScanEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				n.scan(false)
			}
		}
	}()
}

func (n *notifier) scan(baseline bool) {
	if resetStates.Swap(false) {
		for _, l := range n.logs {
			l.running, l.pending = false, nil
		}
	}
	files, _ := filepath.Glob(filepath.Join(n.root, "*", "session-*", sessionLogName))
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			continue
		}
		if l := n.logs[f]; l != nil {
			l.modified = st.ModTime()
		}
		l := n.logs[f]
		if l == nil {
			l = &sessionLog{id: filepath.Base(filepath.Dir(f))}
			n.logs[f] = l
			if baseline {
				l.offset = st.Size() // 只看以后的
				continue
			}
			l.loaded = true // 新会话从头读，头部和标题自然会读到
		}
		if st.Size() < l.offset { // 被截断或重建，当作新的
			*l = sessionLog{id: l.id, loaded: true}
		}
		if st.Size() == l.offset {
			continue
		}
		l.modified = st.ModTime()
		n.readNew(f, l, st.Size())
	}
	if n.onCounts != nil {
		n.onCounts(n.counts(time.Now()))
	}
}

// counts 汇总托盘要显示的会话状态。子代理会话不单独算：它在跑时父会话也在跑。
func (n *notifier) counts(now time.Time) sessionCounts {
	var c sessionCounts
	for _, l := range n.logs {
		if l.depth > 0 {
			continue
		}
		waiting := false
		for _, kind := range l.pending {
			waiting = true
			if kind == "approval" {
				c.approvals++
			} else {
				c.questions++
			}
		}
		if l.running && !waiting && now.Sub(l.modified) > staleRunning {
			l.running = false
		}
		if l.running && !waiting { // 等你处理的已经算在审批/提问里，不重复计
			c.running++
		}
	}
	return c
}

func (n *notifier) readNew(path string, l *sessionLog, size int64) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	buf := make([]byte, size-l.offset)
	if _, err := f.ReadAt(buf, l.offset); err != nil && err != io.EOF {
		return
	}
	out, err := n.dec.DecodeAll(buf, nil)
	if err != nil {
		// 多半是 dsh 正写到一半的帧，下一轮再试；一直解不开就跳过，别卡死在这个文件上
		if l.stuck++; l.stuck >= 5 {
			log.Printf("通知：%s 的新增内容解不开（%v），跳过", l.id, err)
			l.offset, l.stuck, l.partial = size, 0, nil
		}
		return
	}
	if !l.loaded {
		n.loadHeader(path, l) // 读 [0, offset)，必须在推进 offset 之前
	}
	l.offset, l.stuck = size, 0

	data := append(l.partial, out...)
	lines := bytes.Split(data, []byte{'\n'})
	l.partial = append([]byte(nil), lines[len(lines)-1]...)
	for _, line := range lines[:len(lines)-1] {
		n.handle(l, line)
	}
}

// loadHeader 补读启动前就存在的会话：从头解一遍，只为拿到头部（是不是子代理）和当前标题。
// 每个会话最多一次，且只发生在它启动后第一次有新事件时。
func (n *notifier) loadHeader(path string, l *sessionLog) {
	l.loaded = true
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if len(raw) > int(l.offset) {
		raw = raw[:l.offset] // 只看已经处理过的部分，新增部分接下来会正常处理
	}
	out, err := n.dec.DecodeAll(raw, nil)
	if err != nil {
		return
	}
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		var e logEvent
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		switch e.Type {
		case "session":
			l.depth = e.DelegationDepth
		case "session/title":
			var d struct{ Title string }
			if json.Unmarshal(e.Data, &d) == nil && d.Title != "" {
				l.title = d.Title
			}
		}
	}
}

type logEvent struct {
	Type            string          `json:"type"`
	Data            json.RawMessage `json:"data"`
	DelegationDepth int             `json:"delegationDepth"` // 只在头部那行
}

func (n *notifier) handle(l *sessionLog, line []byte) {
	var e logEvent
	if json.Unmarshal(line, &e) != nil {
		return
	}
	switch e.Type {
	case "session":
		l.depth = e.DelegationDepth
	case "session/title":
		var d struct{ Title string }
		if json.Unmarshal(e.Data, &d) == nil && d.Title != "" {
			l.title = d.Title
		}
	case "assistant/message":
		var d struct {
			Message struct {
				Content []struct{ Type, Text string }
			}
		}
		if json.Unmarshal(e.Data, &d) != nil {
			return
		}
		var parts []string
		for _, c := range d.Message.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				parts = append(parts, c.Text)
			}
		}
		if len(parts) > 0 {
			l.reply = strings.Join(parts, "\n")
		}
	case "turn/start":
		l.running = true
	case "approval/decided":
		var d struct{ ID string }
		if json.Unmarshal(e.Data, &d) == nil {
			delete(l.pending, d.ID)
		}
	case "tool/result":
		var d struct {
			Message struct{ ToolCallID string }
		}
		if json.Unmarshal(e.Data, &d) == nil {
			delete(l.pending, d.Message.ToolCallID) // 提问答完了
		}
	case "tool/call":
		var d struct{ Name, Arguments, CallID string }
		if json.Unmarshal(e.Data, &d) != nil || d.Name != "ask_user_question" {
			return
		}
		l.setPending(d.CallID, "question")
		var args struct {
			Questions []struct{ Question, Header string }
		}
		_ = json.Unmarshal([]byte(d.Arguments), &args)
		body := "有问题等你回答"
		if len(args.Questions) > 0 {
			q := args.Questions[0]
			body = firstNonEmpty(q.Question, q.Header, body)
			if len(args.Questions) > 1 {
				body = fmt.Sprintf("%s（共 %d 个问题）", body, len(args.Questions))
			}
		}
		n.notify(l, "question", "❓ 等你回答", body)
	case "approval/asked":
		var d struct{ ID, ToolName, Reason string }
		if json.Unmarshal(e.Data, &d) != nil {
			return
		}
		l.setPending(d.ID, "approval")
		n.notify(l, "approval", "🔐 等你审批", approvalBody(d.ToolName, d.Reason))
	case "turn/end":
		var d struct {
			Reason struct{ Kind string }
		}
		_ = json.Unmarshal(e.Data, &d)
		kind := d.Reason.Kind
		reply := l.reply
		l.reply, l.running, l.pending = "", false, nil
		switch {
		case kind == "completed" || kind == "":
			n.notify(l, "turn", "✅ 已完成", firstNonEmpty(reply, "这一轮已完成"))
		case containsAny(kind, "cancel", "abort", "interrupt", "stop"):
			// 用户自己停的，不用提醒
		default:
			n.notify(l, "turn", "⚠️ 已结束（"+kind+"）", firstNonEmpty(reply, "这一轮没有正常完成"))
		}
	}
}

func (l *sessionLog) setPending(id, kind string) {
	if id == "" {
		id = kind // 老格式没有 id 时，至少保证这一类能被 turn/end 清掉
	}
	if l.pending == nil {
		l.pending = map[string]string{}
	}
	l.pending[id] = kind
}

func (n *notifier) notify(l *sessionLog, kind, status, body string) {
	if !notifyEnabled.Load() || l.depth > 0 {
		return
	}
	// 用户正看着窗口就不打扰
	if n.win != nil && !n.win.IsDestroyed() && n.win.IsVisible() && n.win.IsFocused() {
		return
	}
	title := status
	if l.title != "" {
		title = status + " · " + l.title
	}
	body = truncateRunes(strings.Join(strings.Fields(body), " "), maxNotifyBody)
	log.Printf("通知：%s | %s", title, body)
	n.send("dsh-"+l.id+"-"+kind, title, body) // 同一会话同类通知只留最新一条
}

func showNotification(id, title, body string) {
	note := mygo.NewNotification(mygo.NotificationOptions{ID: id, Title: title, Body: body})
	if err := note.Show(); err != nil {
		log.Printf("通知发送失败：%v", err)
	}
}

// approvalBody 取「工具名：原因的第一句」。dsh 的 reason 形如
// "escalate sandbox to danger-full-access: 该命令需要……，因此……"，前缀是给审计看的。
func approvalBody(tool, reason string) string {
	if _, rest, ok := strings.Cut(reason, ": "); ok && strings.HasPrefix(reason, "escalate ") {
		reason = rest
	}
	if i := strings.IndexAny(reason, "，。；\n"); i > 0 {
		reason = reason[:i]
	}
	switch {
	case tool != "" && reason != "":
		return tool + "：" + reason
	case tool != "":
		return tool + " 需要授权"
	}
	return firstNonEmpty(reason, "有操作需要授权")
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
