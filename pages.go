package main

import (
	"fmt"
	"html"
	"time"
)

// 加载页和报错页：dsh 就绪前窗口里显示的本地 HTML。
// 每换一个阶段就整页重载一次，阶段只有三四个，不值得为此搭一套 JS 通信。

const pageStyle = `
:root { color-scheme: light dark; --fg:#1f2328; --muted:#656d76; --bg:#ffffff; --card:#f6f8fa; --accent:#4d6bfe; --err:#cf222e; }
@media (prefers-color-scheme: dark) { :root { --fg:#e6edf3; --muted:#8d96a0; --bg:#0d1117; --card:#161b22; --err:#ff7b72; } }
* { box-sizing: border-box; }
html, body { margin:0; height:100%; background:var(--bg); color:var(--fg);
  font: 14px/1.6 system-ui, -apple-system, "Noto Sans CJK SC", "PingFang SC", sans-serif; }
body { display:flex; align-items:center; justify-content:center; }
.box { width:min(640px, 90vw); text-align:center; }
h1 { font-size:20px; font-weight:600; margin:16px 0 6px; }
.muted { color:var(--muted); }
.spinner { width:36px; height:36px; margin:0 auto; border-radius:50%;
  border:3px solid var(--card); border-top-color:var(--accent); animation:spin .9s linear infinite; }
@keyframes spin { to { transform:rotate(360deg); } }
.steps { display:inline-block; text-align:left; margin:18px 0 0; padding:0; list-style:none; }
.steps li { margin:4px 0; color:var(--muted); }
.steps li.done::before { content:"✓ "; color:var(--accent); }
.steps li.cur { color:var(--fg); }
.steps li.cur::before { content:"● "; color:var(--accent); }
.steps li.todo::before { content:"○ "; }
.err h1 { color:var(--err); }
pre { text-align:left; background:var(--card); padding:12px; border-radius:8px; max-height:45vh;
  overflow:auto; font:12px/1.5 ui-monospace, "JetBrains Mono", monospace; white-space:pre-wrap; word-break:break-all; }
code { font-family: ui-monospace, monospace; }
.small { font-size:12px; }
.note { display:inline-block; margin:4px 0 8px; padding:4px 10px; border-radius:6px; background:var(--card); color:var(--err); }
.btn { display:inline-block; margin-top:8px; padding:8px 28px; border-radius:8px; background:var(--accent);
  color:#fff; text-decoration:none; font-weight:600; }
.btn:hover { filter:brightness(1.1); }
.btn:active { filter:brightness(.9); }
`

var bootSteps = []string{"启动 dsh", "等待登录地址", "等待后端就绪"}

// retryURL 是报错页「重试」按钮的链接，main 里用 OnWillNavigate 拦截，不会真的跳转。
const retryURL = "dsh-desktop://retry"

// loadingPage 渲染第 step 步（从 0 开始）的加载页。started 用来让计时跨阶段连续；
// note 非空时显示在标题下面（比如崩溃重启的原因）；meta 是一行小字（profile、dsh 版本）。
func loadingPage(step int, started time.Time, note, meta string) string {
	noteHTML := ""
	if note != "" {
		noteHTML = `<div class="note">` + html.EscapeString(note) + `</div>`
	}
	items := ""
	for i, s := range bootSteps {
		cls := "todo"
		switch {
		case i < step:
			cls = "done"
		case i == step:
			cls = "cur"
		}
		items += fmt.Sprintf(`<li class="%s">%s</li>`, cls, html.EscapeString(s))
	}
	return fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><title>DeepSeek Harness</title><style>%s</style></head>
<body><div class="box">
<div class="spinner"></div>
<h1>正在启动 DeepSeek Harness</h1>
%s
<div class="muted">已用 <span id="t">0</span> 秒，首次启动可能需要更久</div>
<div class="muted small">%s</div>
<ul class="steps">%s</ul>
</div>
<script>
const t0 = %d;
const el = document.getElementById("t");
const tick = () => { el.textContent = Math.max(0, Math.floor((Date.now() - t0) / 1000)); };
tick(); setInterval(tick, 500);
</script>
</body></html>`, pageStyle, noteHTML, html.EscapeString(meta), items, started.UnixMilli())
}

// errorPage 渲染启动失败 / dsh 退出时的报错页。
func errorPage(title string, err error, dshTail, logPath string) string {
	tail := ""
	if dshTail != "" {
		tail = `<p class="muted">dsh 最近的输出：</p><pre>` + html.EscapeString(dshTail) + `</pre>`
	}
	return fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><title>DeepSeek Harness - 出错了</title><style>%s</style></head>
<body><div class="box err">
<h1>%s</h1>
<pre>%s</pre>
%s
<p><a class="btn" href="%s">重试</a></p>
<p class="muted">完整日志：<code>%s</code></p>
</div></body></html>`, pageStyle, html.EscapeString(title), html.EscapeString(err.Error()), tail, retryURL, html.EscapeString(logPath))
}
