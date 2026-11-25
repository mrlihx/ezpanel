package pkgmgr

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

// phpMyAdmin 通过短时套接字访问实例内的 localhost 账号。
func (m *Manager) PHPMyAdminInstanceSocket(ctx context.Context, instance DatabaseInstance) (string, func(), error) {
	target := ""
	for _, path := range []string{"/run/mysqld/mysqld.sock", "/tmp/mysql.sock", "/var/lib/mysql/mysql.sock"} {
		candidate := fmt.Sprintf("/proc/%d/root%s", instance.PID, path)
		if info, err := os.Stat(candidate); err == nil && info.Mode()&os.ModeSocket != 0 {
			target = candidate
			break
		}
	}
	if target == "" {
		return "", nil, errors.New("所选 MySQL 实例的本地套接字不可用")
	}
	dir := filepath.Dir(m.MySQLSocket())
	if err := os.MkdirAll(dir, 0710); err != nil {
		return "", nil, err
	}
	if err := m.allowPHPMyAdminMySQLSocket(); err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, "pma-"+rand.Text()+".sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return "", nil, err
	}
	if err = os.Chown(path, 0, phpMyAdminGroup); err == nil {
		err = os.Chmod(path, 0660)
	}
	if err != nil {
		listener.Close()
		return "", nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	stopWatch := context.AfterFunc(ctx, func() { _ = listener.Close() })
	cleanup := func() {
		stopWatch()
		cancel()
		_ = listener.Close()
	}
	go func() {
		defer listener.Close()
		for {
			client, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			go proxyMySQLSocket(ctx, client, target)
		}
	}()
	return path, cleanup, nil
}

func proxyMySQLSocket(ctx context.Context, client *net.UnixConn, target string) {
	defer client.Close()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", target)
	if err != nil {
		return
	}
	server := conn.(*net.UnixConn)
	defer server.Close()
	stop := context.AfterFunc(ctx, func() {
		client.Close()
		server.Close()
	})
	defer stop()
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(server, client)
		_ = server.CloseWrite()
		close(done)
	}()
	_, _ = io.Copy(client, server)
	_ = client.Close()
	<-done
}
