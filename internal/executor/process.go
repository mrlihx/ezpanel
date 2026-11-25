package executor

import (
	"errors"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type ProcessOptions struct {
	Path, Dir string
	Env       []string
	UID, GID  uint32
	Dev       bool
	Output    io.Writer
}
type Process struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
	once sync.Once
}

// StartProcess 是插件长进程的唯一启动入口；调用方提供已经校验的路径和固定环境。
func StartProcess(o ProcessOptions) (*Process, error) {
	if o.Path == "" || o.Dir == "" || len(o.Env) == 0 {
		return nil, errors.New("进程启动配置不完整")
	}
	cmd := exec.Command(o.Path)
	cmd.Dir = o.Dir
	cmd.Env = o.Env
	cmd.Stdout = o.Output
	cmd.Stderr = o.Output
	cmd.WaitDelay = 5 * time.Second
	if e := pluginCredentials(cmd, o); e != nil {
		return nil, e
	}
	if e := cmd.Start(); e != nil {
		return nil, e
	}
	p := &Process{cmd: cmd, done: make(chan struct{})}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	return p, nil
}
func (p *Process) Done() <-chan struct{} { return p.done }
func (p *Process) Stop() {
	p.once.Do(func() {
		pg := -p.cmd.Process.Pid
		syscall.Kill(pg, syscall.SIGTERM)
		deadline := time.Now().Add(5 * time.Second)
		for syscall.Kill(pg, 0) == nil {
			if time.Now().After(deadline) {
				syscall.Kill(pg, syscall.SIGKILL)
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		<-p.done
	})
}
