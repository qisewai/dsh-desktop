package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// dsh 在 stdout 打印的带 token 地址，token 固定 43 字符
var tokenRe = regexp.MustCompile(`(http://(?:127\.0\.0\.1|localhost):\d+/\?token=[A-Za-z0-9_-]{20,})`)

// dshProc 是一个运行中的 dsh web 进程（及其整个进程组）。
type dshProc struct {
	cmd      *exec.Cmd
	tokenCh  chan string   // 拿到带 token 的地址时写入一次
	exited   chan struct{} // cmd.Wait 返回后关闭
	exitErr  error         // exited 关闭后才可读
	stopping atomic.Bool   // 主动 stop 过，用来区分「意外退出」

	tailMu sync.Mutex
	tail   []string // dsh 最近的输出，报错页里展示
}

const tailLines = 40

func startDsh(bin string, port int, profile string) (*dshProc, error) {
	// --profile 必须在最前：`dsh web --port … --profile x` 里的 --profile 会被透传给
	// web 应用，dsh 报 unknown option '--profile' 直接退出（见 profiles.go）
	args := []string{"-oL", "-eL"} // stdbuf 参数
	args = append(args, bin, "--profile", profile, "--port", fmt.Sprint(port), "--no-open")
	cmd := exec.Command("stdbuf", args...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	// 独立进程组：dsh 会再拉 node/pnpm 子进程，整组 kill 才干净
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// 用 os.Pipe 而不是 StdoutPipe：StdoutPipe 要求读完才能 Wait，
	// 而 node 子进程会继承写端，dsh 父进程退了管道也不一定 EOF。
	// 自己管管道，Wait 只等进程，输出由 pump 一直读到 EOF。
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = outW, errW

	if err := cmd.Start(); err != nil {
		outR.Close()
		outW.Close()
		errR.Close()
		errW.Close()
		return nil, err
	}
	outW.Close()
	errW.Close()

	p := &dshProc{
		cmd:     cmd,
		tokenCh: make(chan string, 1),
		exited:  make(chan struct{}),
	}
	// 拿到 token 之后也必须一直读：不读的话管道 64KB 写满，dsh 往 stdout 写日志就会阻塞，
	// 表现为用一阵子后界面突然卡死。
	go p.pump(outR, true)
	go p.pump(errR, false)
	go func() {
		p.exitErr = cmd.Wait()
		close(p.exited)
	}()
	log.Printf("dsh pid: %d", cmd.Process.Pid)
	return p, nil
}

func (p *dshProc) pump(r io.ReadCloser, findToken bool) {
	defer r.Close()
	found := false
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadString('\n')
		if line = strings.TrimRight(line, "\r\n"); line != "" {
			if findToken && !found {
				if m := tokenRe.FindString(line); m != "" {
					found = true
					p.tokenCh <- m
				}
			}
			p.record(maskToken(line)) // token 别落进日志文件
		}
		if err != nil {
			return
		}
	}
}

func (p *dshProc) record(line string) {
	log.Printf("[dsh] %s", line)
	p.tailMu.Lock()
	p.tail = append(p.tail, line)
	if len(p.tail) > tailLines {
		p.tail = p.tail[len(p.tail)-tailLines:]
	}
	p.tailMu.Unlock()
}

// Tail 返回 dsh 最近的输出。
func (p *dshProc) Tail() string {
	p.tailMu.Lock()
	defer p.tailMu.Unlock()
	return strings.Join(p.tail, "\n")
}

// exitError 描述 dsh 的退出原因，只在 exited 关闭后调用。
func (p *dshProc) exitError() error {
	if p.exitErr != nil {
		return fmt.Errorf("dsh 进程已退出：%v", p.exitErr)
	}
	return errors.New("dsh 进程已退出（退出码 0）")
}

// Stop 回收整个进程组，可重复调用。
func (p *dshProc) Stop() {
	p.stopping.Store(true)
	killGroup(p.cmd)
}

// WaitToken 等 dsh 在 stdout 打出带 token 的地址。
func (p *dshProc) WaitToken(ctx context.Context, budget time.Duration) (string, error) {
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case u := <-p.tokenCh:
		return u, nil
	case <-p.exited:
		// 进程退出前可能刚好打出了 token，优先取
		select {
		case u := <-p.tokenCh:
			return u, nil
		default:
		}
		return "", p.exitError()
	case <-timer.C:
		return "", fmt.Errorf("%v 内没等到 token 地址", budget)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// WaitReady 轮询到「带 token → 303 且下发 cookie」。
// dsh 先 bind 端口再拉后端，中间所有请求都是 502 —— 光看端口 open 会误判。
func (p *dshProc) WaitReady(ctx context.Context, authURL string, budget time.Duration) error {
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       5 * time.Second,
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	last := 0
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, authURL, nil)
		if resp, err := client.Do(req); err == nil {
			last = resp.StatusCode
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusSeeOther && len(resp.Cookies()) > 0 {
				return nil
			}
		}
		select {
		case <-p.exited:
			return p.exitError()
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("%v 内未就绪，最后一次 HTTP %d", budget, last)
			}
			return ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
}

func findDsh(path string) (string, error) {
	if path != "" {
		st, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if st.IsDir() || st.Mode()&0o111 == 0 {
			return "", fmt.Errorf("%s 不是可执行文件", path)
		}
		return path, nil
	}
	for _, p := range []string{"/usr/bin/dsh", "/usr/local/bin/dsh"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	if p, err := exec.LookPath("dsh"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("没找到 dsh，请先 npm i -g @deepseek-ai/dsh，或用 -dsh 指定路径")
}

func freePort(start int) (int, error) {
	for p := start; p < start+100; p++ {
		if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p)); err == nil {
			ln.Close()
			return p, nil
		}
	}
	return 0, fmt.Errorf("%d–%d 没有空闲端口，可用 -port 指定", start, start+99)
}

// killGroup 对整个进程组发信号：dsh 带一串 node 子进程，只杀父进程会留孤儿。
// dsh 父进程已经退出时也要调：子进程还在同一个组里。
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pgid := -cmd.Process.Pid
	if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil {
		return // 整组都没了
	}
	for i := 0; i < 50; i++ {
		if err := syscall.Kill(pgid, 0); err != nil {
			log.Println("dsh 进程组已退出")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	log.Println("dsh 未响应 SIGTERM，强杀")
	_ = syscall.Kill(pgid, syscall.SIGKILL)
}

var tokenParamRe = regexp.MustCompile(`token=[A-Za-z0-9_-]+`)

func maskToken(u string) string {
	return tokenParamRe.ReplaceAllStringFunc(u, func(s string) string {
		v := s[len("token="):]
		if len(v) <= 10 {
			return s
		}
		return "token=" + v[:4] + "…" + v[len(v)-4:]
	})
}
