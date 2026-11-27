package hardening

import "strings"

// 监测和内核拦截共用同一套模式语义，避免排除范围不一致。
func exclusionPatterns(text string) []string {
	var patterns []string
	for _, line := range strings.Split(text, "\n") {
		if pattern := strings.TrimSpace(line); pattern != "" {
			patterns = append(patterns, pattern)
		}
	}
	return patterns
}
