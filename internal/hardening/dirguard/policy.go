package dirguard

import (
	"os"
	"path/filepath"
	"strings"
)

type Policy struct {
	ID, Path   string
	Exclusions []string
}

// record 与内核策略映射的布局一致，固定宽度字段保证跨 amd64/arm64 序列化一致。
type record struct {
	Length, Start, Accept, Count uint32
	Device, Inode                uint64
	Initial, Padding             uint32
	Root                         [MaxRootPath]byte
	Next                         [256]uint16
}

// 状态目录第一次启动时可能尚未创建，解析已有上级保证别名不会产生第二套内核策略。
func canonicalStatePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	prefix := absolute
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(prefix)
		if err == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(prefix)
		if parent == prefix {
			return "", err
		}
		suffix = append(suffix, filepath.Base(prefix))
		prefix = parent
	}
}
func mountAnchors(data, root string) []string {
	paths := []string{root}
	seen := map[string]bool{root: true}
	decode := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		path := filepath.Clean(decode.Replace(fields[4]))
		if strings.HasPrefix(path, root+"/") && !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	return paths
}
