package logview

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// 运行日志最多保留当前和上一份各10MiB，标准错误仍交给systemd收集。
type Runtime struct {
	mu   sync.Mutex
	file *os.File
	path string
	size int64
}

func StartRuntime(dataDir string) (*Runtime, error) {
	dir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "runtime.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	r := &Runtime{file: f, path: path, size: info.Size()}
	slog.SetDefault(slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, r), nil)))
	return r, nil
}
func (r *Runtime) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size+int64(len(p)) > 10<<20 {
		rotated := r.path + ".1"
		if err := os.Rename(r.path, rotated); err != nil {
			return 0, err
		}
		f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return 0, errors.Join(err, os.Rename(rotated, r.path))
		}
		_ = r.file.Close()
		r.file = f
		r.size = 0
	}
	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}
func (r *Runtime) Clear() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.file.Truncate(0); err != nil {
		return err
	}
	r.size = 0
	if err := os.Remove(r.path + ".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
func (r *Runtime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.file.Close()
}
