package database

import (
	"context"
	"database/sql"
	"testing"
)

func TestSQLiteRowKeyIgnoresShadowedAlias(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE entry (rowid TEXT, value TEXT, note TEXT);
		INSERT INTO entry (_rowid_, rowid, value)
		VALUES (9007199254740993, 'same', 'first'), (9007199254740994, 'same', 'second');`); err != nil {
		t.Fatal(err)
	}
	cols, _, err := sqliteTableColumns(ctx, db, "entry")
	if err != nil {
		t.Fatal(err)
	}
	key := encodeSQLiteKey([]any{int64(9007199254740993)})
	where, params, err := buildSQLiteWhere(ctx, db, "entry", cols, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqliteChangeRow(ctx, db, "UPDATE entry SET value = ? WHERE "+where, append([]any{"edited"}, params...)...); err != nil {
		t.Fatal(err)
	}
	var value string
	var note sql.NullString
	if err := db.QueryRow("SELECT value, note FROM entry WHERE _rowid_ = 9007199254740993").Scan(&value, &note); err != nil || value != "edited" || note.Valid {
		t.Fatalf("target row = %q, %v, error = %v", value, note, err)
	}
	if err := sqliteChangeRow(ctx, db, "DELETE FROM entry WHERE rowid = 'same'"); err == nil {
		t.Fatal("ambiguous deletion succeeded")
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM entry").Scan(&count); err != nil || count != 2 {
		t.Fatalf("row count = %d, error = %v", count, err)
	}
}

func TestSQLiteCompositeKeyPreservesBlob(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE entry (a INTEGER, b BLOB, PRIMARY KEY (a, b)) WITHOUT ROWID;
		INSERT INTO entry VALUES (1, x'ff00'), (1, x'00ff');`); err != nil {
		t.Fatal(err)
	}
	cols, _, err := sqliteTableColumns(ctx, db, "entry")
	if err != nil {
		t.Fatal(err)
	}
	where, params, err := buildSQLiteWhere(ctx, db, "entry", cols, encodeSQLiteKey([]any{int64(1), []byte{0xff, 0}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqliteChangeRow(ctx, db, "DELETE FROM entry WHERE "+where, params...); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := db.QueryRow("SELECT hex(b) FROM entry").Scan(&value); err != nil || value != "00FF" {
		t.Fatalf("retained key = %q, error = %v", value, err)
	}
}
