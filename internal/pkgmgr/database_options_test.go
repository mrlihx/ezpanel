package pkgmgr

import (
	"archive/tar"
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"ezpanel/internal/executor"
)

func TestMySQLMainConfigReadsSelectedContainer(t *testing.T) {
	m := New()
	m.LookPath = func(string) (string, error) { return "/usr/bin/docker", nil }
	const config = "[mysqld]\nmax_connections=151\n!includedir /etc/mysql/conf.d/\n"
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{Name: "my.cnf", Mode: 0644, Size: int64(len(config))}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(config)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	m.Run = func(_ context.Context, _ time.Duration, _ []string, name string, args ...string) (executor.Result, error) {
		if name != "/usr/bin/docker" || strings.Join(args, " ") != "cp -L selected:/etc/my.cnf -" {
			t.Fatalf("unexpected command: %s %v", name, args)
		}
		return executor.Result{Stdout: archive.String()}, nil
	}
	got, err := m.mysqlMainConfig(context.Background(), "selected")
	if err != nil || string(got) != config {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestMySQLOptionsPreserveClientSectionAndBinlogPath(t *testing.T) {
	const old = "[client]\nmax_connections=12\n[mysqld]\nmax-connections=80\nlog-bin=/var/lib/mysql/custom\n"
	disabled, err := editDatabaseOptions("mysql", old, map[string]string{"max_connections": "150", "binlog": "0"})
	if err != nil || !strings.Contains(disabled, "[client]\nmax_connections=12\n") ||
		!strings.Contains(disabled, "log-bin = /var/lib/mysql/custom\nskip-log-bin\n") ||
		strings.Contains(disabled, "max-connections=80") {
		t.Fatalf("disabled = %q, %v", disabled, err)
	}
	enabled, err := editDatabaseOptions("mysql", disabled, map[string]string{"binlog": "1"})
	if err != nil || strings.Contains(enabled, "skip-log-bin") ||
		strings.Count(enabled, "[mysqld]") != 1 || strings.Count(enabled, "log-bin = /var/lib/mysql/custom") != 1 {
		t.Fatalf("enabled = %q, %v", enabled, err)
	}
}

func TestMongoOptionsPreserveUnrelatedSettings(t *testing.T) {
	const original = "storage:\n  dbPath: /data/db\nnet:\n  bindIpAll: true\n"
	got, err := editDatabaseOptions("mongodb", original, map[string]string{
		"authorization":     "disabled",
		"cacheSizeGB":       "1.5",
		"profilingMode":     "slowOp",
		"slowOpThresholdMs": "150",
	})
	if err != nil {
		t.Fatal(err)
	}
	values := mongoOptionValues(got)
	if values["authorization"] != "disabled" || values["cacheSizeGB"] != "1.5" ||
		values["profilingMode"] != "slowOp" || values["slowOpThresholdMs"] != "150" ||
		values["dbPath"] != "/data/db" || !strings.Contains(got, "bindIpAll: true") {
		t.Fatalf("config = %s", got)
	}
}
