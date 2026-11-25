package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpLogClearKeepsRecordsWhenRewriteFails(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	path := filepath.Join(t.TempDir(), "op_logs.jsonl")
	SetOpLogPath(db, path)
	for _, when := range []time.Time{time.Unix(2, 0), time.Unix(1, 0)} {
		if err := db.Create(&OpLog{Module: "test", CreatedAt: when}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ClearOpLogs(db, OpLogFilter{}, nil); err == nil {
		t.Fatal("clearing unexpectedly succeeded")
	}
	rows, total := QueryOpLogs(db, OpLogFilter{Page: 1, Size: 1})
	if total != 2 || len(rows) != 1 || rows[0].CreatedAt.Unix() != 2 {
		t.Fatalf("logs after failed clear = %+v, total = %d", rows, total)
	}
	ascRows, ascTotal := QueryOpLogs(db, OpLogFilter{OrderAsc: true})
	if ascTotal != 2 || len(ascRows) != 2 || ascRows[0].CreatedAt.Unix() != 1 || ascRows[1].CreatedAt.Unix() != 2 {
		t.Fatalf("asc logs = %+v, total = %d", ascRows, ascTotal)
	}
}
