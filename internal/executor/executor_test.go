package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestChild(t *testing.T) {
	if os.Getenv("EXEC_TEST") != "1" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "binary":
		os.Stdout.Write([]byte{0, 1, 2, 255})
		fmt.Fprint(os.Stderr, "error")
	case "long":
		fmt.Println(strings.Repeat("a", 1200000))
		fmt.Println("done")
	case "group":
		child := exec.Command(os.Args[0], "-test.run=TestChild", "ignore")
		child.Env = os.Environ()
		child.Start()
		fmt.Println(child.Process.Pid)
		time.Sleep(time.Minute)
	case "ignore":
		signal.Ignore(syscall.SIGTERM)
		time.Sleep(time.Minute)
	case "sleep":
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}
func TestStreams(t *testing.T) {
	env := append(os.Environ(), "EXEC_TEST=1", "GORACE=atexit_sleep_ms=0")
	var b bytes.Buffer
	r, e := Pipe(context.Background(), time.Second, env, nil, &b, os.Args[0], "-test.run=TestChild", "binary")
	if e != nil || !bytes.Equal(b.Bytes(), []byte{0, 1, 2, 255}) || r.Stderr != "error" {
		t.Fatal(r, e, b.Bytes())
	}
	lines := []string{}
	r, e = Stream(context.Background(), time.Second, env, func(s string) { lines = append(lines, s) }, os.Args[0], "-test.run=TestChild", "long")
	if e != nil || !r.Truncated || len(lines) != 2 || len(lines[0]) > 4096 || lines[1] != "done" {
		t.Fatal(r, e, len(lines))
	}
	r, e = RunEnv(context.Background(), time.Second, env, os.Args[0], "-test.run=TestChild", "long")
	if e != nil || !r.Truncated || len(r.Stdout) > 1<<20 {
		t.Fatal(r.Truncated, e)
	}
	_, e = RunEnv(context.Background(), 30*time.Millisecond, env, os.Args[0], "-test.run=TestChild", "sleep")
	if e == nil {
		t.Fatal("timeout")
	}
}
func TestPTYForeground(t *testing.T) {
	s, e := StartPTY(context.Background(), "/bin/bash", t.TempDir(), 80, 24)
	if e != nil {
		t.Fatal(e)
	}
	s.File.Write([]byte("sleep 60\n"))
	var fg int
	for i := 0; i < 100; i++ {
		fg, _ = unix.IoctlGetInt(int(s.File.Fd()), unix.TIOCGPGRP)
		if fg > 0 && fg != s.cmd.Process.Pid {
			break
		}
		time.Sleep(time.Millisecond * 10)
	}
	if fg <= 0 || fg == s.cmd.Process.Pid {
		s.Close()
		t.Fatal("no foreground job")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	if e = syscall.Kill(-fg, 0); e == nil {
		t.Fatal("foreground survived")
	}
	if _, ok := s.ExitCode(); !ok {
		t.Fatal("not reaped")
	}
}

func TestTimeoutKillsGroup(t *testing.T) {
	env := append(os.Environ(), "EXEC_TEST=1", "GORACE=atexit_sleep_ms=0")
	r, e := RunEnv(context.Background(), 100*time.Millisecond, env, os.Args[0], "-test.run=TestChild", "group")
	if e == nil {
		t.Fatal("not timed out")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(r.Stdout))
	if err != nil {
		t.Fatal(r, err)
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	for i := 0; i < 100; i++ {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("descendant survived cancellation")
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }
func TestPipeWriterFailure(t *testing.T) {
	env := append(os.Environ(), "EXEC_TEST=1", "GORACE=atexit_sleep_ms=0")
	_, e := Pipe(context.Background(), time.Second, env, nil, failingWriter{}, os.Args[0], "-test.run=TestChild", "binary")
	if e == nil {
		t.Fatal("stdout write failure ignored")
	}
}

func TestPipeCancellationClosesStreams(t *testing.T) {
	for _, direction := range []string{"stdin", "stdout"} {
		t.Run(direction, func(t *testing.T) {
			r, w := io.Pipe()
			defer r.Close()
			defer w.Close()
			env := append(os.Environ(), "EXEC_TEST=1", "GORACE=atexit_sleep_ms=0")
			done := make(chan error, 1)
			go func() {
				var e error
				if direction == "stdin" {
					_, e = Pipe(context.Background(), 80*time.Millisecond, env, r, nil, os.Args[0], "-test.run=TestChild", "sleep")
				} else {
					_, e = Pipe(context.Background(), 80*time.Millisecond, env, nil, w, os.Args[0], "-test.run=TestChild", "binary")
				}
				done <- e
			}()
			select {
			case e := <-done:
				if !errors.Is(e, context.DeadlineExceeded) {
					t.Fatal(e)
				}
			case <-time.After(700 * time.Millisecond):
				r.Close()
				w.Close()
				<-done
				t.Fatal("timeout blocked waiting for caller stream")
			}
		})
	}
}

type ownedReader struct {
	*strings.Reader
	closed bool
}

func (s *ownedReader) Close() error { s.closed = true; return nil }

type ownedWriter struct {
	bytes.Buffer
	closed bool
}

func (s *ownedWriter) Close() error { s.closed = true; return nil }
func TestPipeNormalCompletionKeepsStreams(t *testing.T) {
	r := &ownedReader{Reader: strings.NewReader("finite")}
	w := &ownedWriter{}
	env := append(os.Environ(), "EXEC_TEST=1", "GORACE=atexit_sleep_ms=0")
	_, e := Pipe(context.Background(), time.Second, env, r, w, os.Args[0], "-test.run=TestChild", "binary")
	if e != nil || r.closed || w.closed {
		t.Fatal(e, r.closed, w.closed)
	}
}
