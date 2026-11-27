package dirguard

import "testing"

func TestFutureExclusions(t *testing.T) {
	for _, test := range []struct {
		pattern, path string
		want          bool
	}{
		{"*.log", "new.log", true}, {"*.log", "a/b/new.log", true}, {"*.log", "new.txt", false},
		{"uploads/**", "uploads", true}, {"uploads/**", "uploads/new/a.png", true}, {"uploads/**", "uploads-other/a.png", false},
		{"**/cache/*.tmp", "cache/new.tmp", true}, {"**/cache/*.tmp", "a/cache/new.tmp", true}, {"**/cache/*.tmp", "a/notcache/new.tmp", false},
		{"data/?.txt", "data/中.txt", true}, {"data/?.txt", "data/ab.txt", false}, {"a/[0-9].log", "a/5.log", true},
		{"**", "", false},
	} {
		m, err := Compile([]string{test.pattern})
		if err != nil {
			t.Fatal(err)
		}
		if got := m.Match(test.path); got != test.want {
			t.Errorf("%q match %q=%v", test.pattern, test.path, got)
		}
	}
}
