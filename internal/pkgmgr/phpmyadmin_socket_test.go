package pkgmgr

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProxyMySQLSocketForwardsBothWays(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "ezpanel-socket-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	listen := func(name string) *net.UnixListener {
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, name), Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
		return listener
	}
	db, proxy := listen("db"), listen("proxy")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		conn, err := db.AcceptUnix()
		if err == nil {
			defer conn.Close()
			if request, err := io.ReadAll(conn); err == nil && string(request) == "hello" {
				_, _ = conn.Write([]byte("response"))
			}
		}
	}()
	go func() {
		conn, err := proxy.AcceptUnix()
		if err == nil {
			proxyMySQLSocket(ctx, conn, db.Addr().String())
		}
	}()
	conn, err := net.DialUnix("unix", nil, proxy.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := conn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(conn)
	if err != nil || string(response) != "response" {
		t.Fatalf("response = %q, error = %v", response, err)
	}
}
