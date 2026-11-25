package task

import (
	"path/filepath"
	"testing"

	"ezpanel/internal/store"
)

func TestCronLogRangesRemainReadableAfterCompacting(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close()
	m := &Manager{db: db}
	path := filepath.Join(dir, "cron-test.log")
	var rows []store.Task
	appendRun := func(line string) {
		file, start, err := openCronLog(path)
		if err != nil {
			t.Fatal(err)
		}
		n, err := file.WriteString(line + "\n")
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
		size := int64(n)
		row := store.Task{ID: store.ID(), Module: "cron", Status: "success", LogPath: path, LogStart: start, LogSize: &size}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	check := func(row store.Task, expected string) {
		lines, _, err := m.ReadLogs(row, 0, 20)
		if err != nil || len(lines) != 1 || lines[0] != expected {
			t.Fatalf("日志范围错误，读取结果 %v，错误 %v", lines, err)
		}
	}

	appendRun("first")
	appendRun("second")
	check(rows[0], "first")
	check(rows[1], "second")
	if err := m.RemoveLogs(rows[0]); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&rows[0]).Error; err != nil {
		t.Fatal(err)
	}
	if err := m.CompactCronLog(path); err != nil {
		t.Fatal(err)
	}
	check(rows[1], "second")
	appendRun("third")
	check(rows[1], "second")
	check(rows[2], "third")
}
