//go:build windows

package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Windows 上没有进程组信号，用 Job Object 代替：dsh（npm 装的是 dsh.cmd → cmd.exe → node）
// 和它拉起的 node 子进程都在同一个 job 里，回收时整个 job 一起终止。job 设了
// KILL_ON_JOB_CLOSE，壳自己被任务管理器杀掉、句柄被系统关闭时，dsh 也跟着走，不留孤儿占端口。
//
// 不用 stdbuf：Windows 没有它，node 写管道也不经过 libc 的块缓冲。

const createNoWindow = 0x08000000

// dshCommand 组装启动 dsh 的命令。
func dshCommand(bin string, args ...string) *exec.Cmd {
	cmd := exec.Command(bin, args...)
	hideConsole(cmd)
	return cmd
}

// hideConsole：打包出来的壳是 GUI 程序、没有控制台，cmd.exe / node 这类控制台程序
// 启动时会各自弹一个黑窗口。CREATE_NO_WINDOW 让它们在后台跑。
func hideConsole(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
	cmd.SysProcAttr.HideWindow = true
}

// dshCandidates 是 PATH 之前先找的位置：npm 全局安装的默认目录。从开始菜单启动时
// PATH 是登录时的快照，刚装完 Node 还没重新登录的话里面没有 npm 目录。
func dshCandidates() []string {
	var c []string
	if d := os.Getenv("APPDATA"); d != "" {
		c = append(c, filepath.Join(d, "npm", "dsh.cmd"))
	}
	if d := os.Getenv("ProgramFiles"); d != "" {
		c = append(c, filepath.Join(d, "nodejs", "dsh.cmd"))
	}
	return c
}

var (
	kernel32                      = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW          = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject   = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject  = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject        = kernel32.NewProc("TerminateJobObject")
	procQueryInformationJobObject = kernel32.NewProc("QueryInformationJobObject")
)

const (
	jobObjectBasicAccountingInformation = 1
	jobObjectExtendedLimitInformation   = 9
	jobObjectLimitKillOnJobClose        = 0x2000
	processSetQuota                     = 0x0100
	processTerminate                    = 0x0001
)

type jobBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobExtendedLimitInformation struct {
	BasicLimitInformation jobBasicLimitInformation
	IoInfo                [6]uint64
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type jobBasicAccountingInformation struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

// procGroup 是装着 dsh 及其子进程的 job。建 job 失败时 job 为 0，退回到按进程树杀。
type procGroup struct {
	pid int
	mu  sync.Mutex
	job syscall.Handle
}

// newProcGroup 在 cmd.Start 之后调用。dsh 在被放进 job 之前的一瞬间就拉起的子进程不在 job 里，
// 但 dsh.cmd 要先起 cmd.exe 再起 node，node 加载完才会再拉子进程，实际赶不上。
func newProcGroup(cmd *exec.Cmd) *procGroup {
	g := &procGroup{pid: cmd.Process.Pid}
	job, err := createKillOnCloseJob()
	if err != nil {
		log.Printf("建 Job Object 失败，回收时改用 taskkill：%v", err)
		return g
	}
	h, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(g.pid))
	if err != nil {
		syscall.CloseHandle(job)
		log.Printf("打不开 dsh 进程，回收时改用 taskkill：%v", err)
		return g
	}
	defer syscall.CloseHandle(h)
	if r, _, err := procAssignProcessToJobObject.Call(uintptr(job), uintptr(h)); r == 0 {
		syscall.CloseHandle(job)
		log.Printf("dsh 进不了 Job Object，回收时改用 taskkill：%v", err)
		return g
	}
	g.job = job
	return g
}

func createKillOnCloseJob() (syscall.Handle, error) {
	r, _, err := procCreateJobObjectW.Call(0, 0)
	if r == 0 {
		return 0, err
	}
	job := syscall.Handle(r)
	var info jobExtendedLimitInformation
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	if r, _, err := procSetInformationJobObject.Call(uintptr(job), jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)); r == 0 {
		syscall.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// kill 终止整个 job，等里面的进程都退干净（端口、profile 锁才放开）再返回。可重复调用。
// Windows 没有给控制台程序的 SIGTERM，没控制台的 node 收不到 Ctrl+Break，只能直接终止。
func (g *procGroup) kill() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pid == 0 {
		return // 已经回收过
	}
	if g.job == 0 {
		// 退路：按进程树杀。dsh 父进程已经退了的话树断了，子进程杀不到
		c := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(g.pid))
		hideConsole(c)
		_ = c.Run()
		g.pid = 0
		return
	}
	if r, _, err := procTerminateJobObject.Call(uintptr(g.job), 1); r == 0 {
		log.Printf("终止 dsh 的 Job Object 失败：%v", err)
	}
	for i := 0; i < 50; i++ {
		if g.activeProcesses() == 0 {
			log.Println("dsh 进程已全部退出")
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	syscall.CloseHandle(g.job)
	g.job = 0
	g.pid = 0
}

func (g *procGroup) activeProcesses() uint32 {
	var info jobBasicAccountingInformation
	r, _, _ := procQueryInformationJobObject.Call(uintptr(g.job), jobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), 0)
	if r == 0 {
		return 0
	}
	return info.ActiveProcesses
}
