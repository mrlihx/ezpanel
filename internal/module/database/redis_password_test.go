package database

import (
	"errors"
	"slices"
	"testing"
)

func TestSaveRedisPasswordRestoresStartupFile(t *testing.T) {
	failure := errors.New("写入失败")
	for _, failFile := range []bool{false, true} {
		var writes []string
		saved := false
		err := saveRedisPassword("new", "old", func(password string) error {
			writes = append(writes, password)
			if failFile && password == "new" {
				return failure
			}
			return nil
		}, func() error {
			saved = true
			return failure
		})
		if !errors.Is(err, failure) || !slices.Equal(writes, []string{"new", "old"}) || saved == failFile {
			t.Fatalf("file error=%t, writes=%v, save called=%t, err=%v", failFile, writes, saved, err)
		}
	}
}
