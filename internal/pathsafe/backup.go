package pathsafe

import (
	"strings"
	"unicode"
)

// BackupName 将目标名称转换成安全的归档路径片段。
func BackupName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._-", r) {
			return r
		}
		return '_'
	}, name)
	name = strings.Trim(name, "._-")
	if name == "" {
		return "backup"
	}
	if len(name) > 200 {
		name = strings.ToValidUTF8(name[:200], "")
	}
	return name
}
