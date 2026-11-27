//go:build linux

package dirguard

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeDirectoryPolicies(t *testing.T) {
	if os.Getenv("EZPANEL_TEST_DIRECTORY_LSM") != "1" {
		t.Skip("仅在专用 Linux 开发环境显式启用")
	}
	root := t.TempDir()
	t.Cleanup(func() { os.RemoveAll(root + "-renamed"); os.RemoveAll(root + "-exchange") })
	protected := filepath.Join(root, "site")
	if err := os.Mkdir(protected, 0700); err != nil {
		t.Fatal(err)
	}
	regular := filepath.Join(protected, "index.html")
	if err := os.WriteFile(regular, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	opened, err := os.OpenFile(regular, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	control, err := unix.Mmap(int(opened.Fd()), 0, 4096, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		t.Fatal("测试前置可写映射失败", err)
	}
	unix.Munmap(control)
	readMapping, err := unix.Mmap(int(opened.Fd()), 0, 4096, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Munmap(readMapping)
	longDir := protected
	for len(longDir) < 1500 {
		longDir = filepath.Join(longDir, strings.Repeat("nested", 20))
		if err = os.Mkdir(longDir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	guard, err := New(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := guard.Sync(nil); err != nil {
			t.Error(err)
		}
		guard.Close()
	})
	if supported, why := guard.Supported(); !supported {
		t.Fatal(why)
	}
	alias := filepath.Join(root, "alias")
	if err = os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if err = guard.Sync([]Policy{{ID: "test", Path: filepath.Join(alias, "site"), Exclusions: []string{"uploads/**", "*.log", "**/cache/**"}}}); err != nil {
		t.Fatal(err)
	}
	blocked := func(name string, operation func() error) {
		t.Helper()
		if err := operation(); err == nil {
			t.Fatalf("%s 未被实时阻止", name)
		}
	}
	blocked("保护文件硬链接到排除路径", func() error { return os.Link(regular, filepath.Join(protected, "alias.log")) })
	blocked("保护文件硬链接到外部路径", func() error { return os.Link(regular, filepath.Join(root, "outside-alias")) })
	blocked("祖先目录重命名", func() error { return os.Rename(root, root+"-renamed") })
	exchange := root + "-exchange"
	if err = os.Mkdir(exchange, 0700); err != nil {
		t.Fatal(err)
	}
	blocked("交换至保护目录祖先", func() error {
		return unix.Renameat2(unix.AT_FDCWD, exchange, unix.AT_FDCWD, root, unix.RENAME_EXCHANGE)
	})
	outside := filepath.Join(root, "outside-original")
	if err = os.WriteFile(outside, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(outside, filepath.Join(root, "outside-allowed")); err != nil {
		t.Fatal("未保护硬链接被误拒绝", err)
	}
	blocked("新建文件", func() error { return os.WriteFile(filepath.Join(protected, "new.txt"), []byte("new"), 0600) })
	blocked("新建目录", func() error { return os.Mkdir(filepath.Join(protected, "newdir"), 0700) })
	blocked("覆盖文件", func() error { return os.WriteFile(regular, []byte("changed"), 0600) })
	blocked("已有写句柄", func() error { _, err := opened.WriteString("changed"); return err })
	blocked("删除文件", func() error { return os.Remove(regular) })
	blocked("重命名文件", func() error { return os.Rename(regular, filepath.Join(root, "moved")) })
	blocked("修改权限", func() error { return os.Chmod(regular, 0644) })
	blocked("截断文件", func() error { return os.Truncate(regular, 0) })
	blocked("已有句柄截断", func() error { return opened.Truncate(0) })
	if err = os.WriteFile(filepath.Join(protected, "future.log"), []byte("log"), 0600); err != nil {
		t.Fatal("未来通配符文件未放行", err)
	}
	upload := filepath.Join(protected, "uploads")
	if err = os.Mkdir(upload, 0700); err != nil {
		t.Fatal("未来排除目录未放行", err)
	}
	if err = os.WriteFile(filepath.Join(upload, "new.png"), []byte("image"), 0600); err != nil {
		t.Fatal("排除目录子文件未放行", err)
	}
	if err = os.WriteFile(filepath.Join(root, "site-neighbor"), []byte("outside"), 0600); err != nil {
		t.Fatal("保护目录外被错误阻止", err)
	}
	if _, err = unix.Mmap(int(opened.Fd()), 0, 4096, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED); err == nil {
		t.Fatal("共享可写映射未阻止")
	}
	if err = unix.Mprotect(readMapping, unix.PROT_READ|unix.PROT_WRITE); err == nil {
		t.Fatal("已有映射提权未阻止")
	}
	blocked("长路径新增", func() error { return os.WriteFile(filepath.Join(longDir, "blocked.txt"), nil, 0600) })
	if err = os.WriteFile(filepath.Join(longDir, "future.log"), nil, 0600); err != nil {
		t.Fatal("长路径通配符未放行", err)
	}
	if err = os.Mkdir(filepath.Join(protected, "cache"), 0700); err != nil {
		t.Fatal("** 零层目录未放行", err)
	}
	if err = os.WriteFile(filepath.Join(protected, "cache", "new.data"), nil, 0600); err != nil {
		t.Fatal("** 目录后代未放行", err)
	}

	if err = guard.Close(); err != nil {
		t.Fatal(err)
	}
	blocked("面板退出后仍保护", func() error { return os.WriteFile(filepath.Join(protected, "after-close"), nil, 0600) })
	reopened, err := New(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = reopened.Sync(nil); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(protected, "after-recover"), nil, 0600); err != nil {
		t.Fatal("恢复后仍被阻止", err)
	}
}
