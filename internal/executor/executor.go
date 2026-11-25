package executor

import (
	"bytes"
	"context"
	"errors"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type Result struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Truncated bool
}
type capture struct {
	mu        sync.Mutex
	out, err  bytes.Buffer
	n         int
	truncated bool
}
type sink struct {
	c      *capture
	stderr bool
}

func (s sink) Write(b []byte) (int, error) {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	n := len(b)
	left := (1 << 20) - s.c.n
	if len(b) > left {
		s.c.truncated = true
		b = b[:left]
	}
	if s.stderr {
		s.c.err.Write(b)
	} else {
		s.c.out.Write(b)
	}
	s.c.n += len(b)
	return n, nil
}
func Run(ctx context.Context, timeout time.Duration, name string, args ...string) (Result, error) {
	return RunEnv(ctx, timeout, nil, name, args...)
}
func RunEnv(ctx context.Context, timeout time.Duration, env []string, name string, args ...string) (Result, error) {
	return execute(ctx, timeout, env, "", nil, nil, nil, name, args...)
}

// Pipe超时或取消时会关闭调用方提供的io.Closer，正常结束不关闭。
// 阻塞流必须提供Close且Close能解除Read/Write阻塞；非Closer必须自行保证读写及时返回。
func Pipe(ctx context.Context, timeout time.Duration, env []string, stdin io.Reader, stdout io.Writer, name string, args ...string) (Result, error) {
	return execute(ctx, timeout, env, "", stdin, stdout, nil, name, args...)
}
func Stream(ctx context.Context, timeout time.Duration, env []string, onLine func(string), name string, args ...string) (Result, error) {
	return execute(ctx, timeout, env, "", nil, nil, onLine, name, args...)
}

type lineWriter struct {
	pending   []byte
	truncated bool
	onLine    func(string)
	mu        *sync.Mutex
}

func (w *lineWriter) Write(b []byte) (int, error) {
	for _, c := range b {
		if c == '\n' {
			w.flush()
		} else if len(w.pending) < 4096 {
			w.pending = append(w.pending, c)
		} else {
			w.truncated = true
		}
	}
	return len(b), nil
}
func (w *lineWriter) flush() {
	w.mu.Lock()
	w.onLine(string(w.pending))
	w.mu.Unlock()
	w.pending = nil
}
func execute(ctx context.Context, timeout time.Duration, env []string, cwd string, stdin io.Reader, stdout io.Writer, onLine func(string), name string, args ...string) (Result, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.Command(name, args...)
	cmd.Dir = cwd
	cmd.WaitDelay = 5 * time.Second
	if env == nil {
		env = DefaultEnv()
	}
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c := &capture{}
	cmd.Stdout = sink{c: c}
	cmd.Stderr = sink{c: c, stderr: true}
	var lines []*lineWriter
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if onLine != nil {
		mu := &sync.Mutex{}
		lines = []*lineWriter{{onLine: onLine, mu: mu}, {onLine: onLine, mu: mu}}
		cmd.Stdout = lines[0]
		cmd.Stderr = lines[1]
	}
	if e := cmd.Start(); e != nil {
		return Result{ExitCode: -1}, e
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var e error
	waited, cleanup := false, false
	select {
	case e = <-done:
		waited = true
		cleanup = errors.Is(e, exec.ErrWaitDelay)
	case <-ctx.Done():
		e = ctx.Err()
		cleanup = true
	}
	if cleanup {
		// WaitDelay只能关闭exec内部管道，外部流仍需由其Close解除阻塞。
		if stream, ok := stdin.(io.Closer); ok {
			stream.Close()
		}
		if stream, ok := stdout.(io.Closer); ok {
			stream.Close()
		}
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		// 组长提前退出不代表组内作业已退出，仍需对原进程组完成清理。
		deadline := time.Now().Add(5 * time.Second)
		for {
			if !waited {
				select {
				case <-done:
					waited = true
				default:
				}
			}
			if syscall.Kill(-cmd.Process.Pid, 0) != nil {
				break
			}
			if time.Now().After(deadline) {
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !waited {
			<-done
		}
	}
	for _, w := range lines {
		if w.truncated {
			c.truncated = true
		}
		if len(w.pending) > 0 {
			w.flush()
		}
	}
	return Result{Stdout: c.out.String(), Stderr: c.err.String(), ExitCode: cmd.ProcessState.ExitCode(), Truncated: c.truncated}, e
}

type PTY struct {
	File *os.File
	cmd  *exec.Cmd
	done chan struct{}
	err  error
	once sync.Once
}

func StartPTY(ctx context.Context, shell, cwd string, cols, rows uint16) (*PTY, error) {
	if cols == 0 || rows == 0 {
		return nil, errors.New("终端尺寸无效")
	}
	cmd := exec.Command(shell, "-l")
	cmd.Dir = cwd
	cmd.Env = append(DefaultEnv(), "TERM=xterm-256color", "HOME=/root", "SHELL="+shell)
	f, e := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if e != nil {
		return nil, e
	}
	s := &PTY{File: f, cmd: cmd, done: make(chan struct{})}
	go func() { s.err = cmd.Wait(); close(s.done) }()
	// pty 初始化 ioctl 使用 Fd()，会把原文件切为阻塞；新描述符必须重新登记 netpoll。
	raw, e := f.SyscallConn()
	fd := -1
	var setupErr error
	if e == nil {
		e = raw.Control(func(master uintptr) {
			fd, setupErr = unix.FcntlInt(master, unix.F_DUPFD_CLOEXEC, 0)
			if setupErr == nil {
				setupErr = unix.SetNonblock(fd, true)
			}
		})
	}
	if e == nil {
		e = setupErr
	}
	if e != nil {
		if fd >= 0 {
			unix.Close(fd)
		}
		s.Close()
		return nil, e
	}
	f.Close()
	s.File = os.NewFile(uintptr(fd), "pty-master")
	// 不支持 poll/deadline 的平台明确失败，不能交付不可取消的读写。
	if e = s.File.SetDeadline(time.Time{}); e != nil {
		s.Close()
		return nil, e
	}

	go func() {
		select {
		case <-ctx.Done():
			s.Close()
		case <-s.done:
		}
	}()
	return s, nil
}
func (s *PTY) Wait() error { <-s.done; return s.err }
func (s *PTY) Resize(cols, rows uint16) error {
	raw, e := s.File.SyscallConn()
	if e != nil {
		return e
	}
	var resizeErr error
	e = raw.Control(func(fd uintptr) {
		resizeErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Col: cols, Row: rows})
	})
	if e != nil {
		return e
	}
	return resizeErr
}
func (s *PTY) Done() <-chan struct{} { return s.done }
func (s *PTY) ExitCode() (int, bool) {
	select {
	case <-s.done:
		return s.cmd.ProcessState.ExitCode(), true
	default:
		return 0, false
	}
}
func (s *PTY) Close() error {
	var e error
	s.once.Do(func() {
		// 前台作业通常拥有独立进程组，必须在关闭终端之前读取它。
		fg := 0
		if raw, err := s.File.SyscallConn(); err == nil {
			_ = raw.Control(func(fd uintptr) { fg, _ = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP) })
		}
		groups := []int{s.cmd.Process.Pid}
		if fg > 0 && fg != s.cmd.Process.Pid {
			groups = append(groups, fg)
		}
		for _, pg := range groups {
			syscall.Kill(-pg, syscall.SIGHUP)
			syscall.Kill(-pg, syscall.SIGTERM)
		}
		e = s.File.Close()
		deadline := time.Now().Add(5 * time.Second)
		for {
			alive := false
			for _, pg := range groups {
				if syscall.Kill(-pg, 0) == nil {
					alive = true
				}
			}
			if !alive {
				break
			}
			if time.Now().After(deadline) {
				for _, pg := range groups {
					syscall.Kill(-pg, syscall.SIGKILL)
				}
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		<-s.done
	})
	return e
}

// 固定受控搜索路径，不继承服务进程中的凭据与任意 PATH。
func DefaultEnv() []string {
	return []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8"}
}
func LookPath(name string) (string, error) {
	for _, dir := range []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
		p := dir + "/" + name
		if s, e := os.Stat(p); e == nil && !s.IsDir() && s.Mode()&0111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// RunFixed 为受控 Host 命令固定 cwd/env，仍沿用超时和输出上限。
func RunFixed(ctx context.Context, timeout time.Duration, cwd string, env []string, name string, args ...string) (Result, error) {
	return execute(ctx, timeout, env, cwd, nil, nil, nil, name, args...)
}

// StreamFixed 供源码构建使用固定工作目录，沿用进程组取消与日志流。
func StreamFixed(ctx context.Context, timeout time.Duration, cwd string, env []string, onLine func(string), name string, args ...string) (Result, error) {
	return execute(ctx, timeout, env, cwd, nil, nil, onLine, name, args...)
}
