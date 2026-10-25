//go:build !windows

package main

import (
	"log"
	"os/exec"
	"syscall"
	"time"
)

// dshCommand 组装启动 dsh 的命令。
// stdbuf -oL -eL：dsh 的 stdout 接管道时是块缓冲，token 那行永远读不到，等到超时。
// 独立进程组：dsh 会再拉 node/pnpm 子进程，整组 kill 才干净。
func dshCommand(bin string, args ...string) *exec.Cmd {
	cmd := exec.Command("stdbuf", append([]string{"-oL", "-eL", bin}, args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// hideConsole 只在 Windows 上有事做。
func hideConsole(*exec.Cmd) {}

// dshCandidates 是 PATH 之前先找的位置。
func dshCandidates() []string {
	return []string{"/usr/bin/dsh", "/usr/local/bin/dsh"}
}

// procGroup 是 dsh 所在的进程组，pgid 就是 dsh 的 pid。
type procGroup struct{ pgid int }

func newProcGroup(cmd *exec.Cmd) *procGroup { return &procGroup{pgid: cmd.Process.Pid} }

// kill 对整个进程组发信号：dsh 带一串 node 子进程，只杀父进程会留孤儿。
// dsh 父进程已经退出时也要调：子进程还在同一个组里。
func (g *procGroup) kill() {
	pgid := -g.pgid
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
