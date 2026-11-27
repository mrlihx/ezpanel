package settings

import "testing"

func TestHardeningNavigation(t *testing.T) {
	if !validNavigation("security", "hardening") {
		t.Fatal("系统加固标签应支持保存与恢复")
	}
	if validNavigation("security", "hardening/unknown") {
		t.Fatal("不应接受未注册的加固子路由")
	}
}

func TestApplicationLogNavigation(t *testing.T) {
	for _, value := range []string{"apps/frp", "apps/frp/frpc", "apps/frp/frps", "apps/supervisor/main", "apps/supervisor/0123456789abcdef"} {
		if !validNavigation("oplogs", value) {
			t.Errorf("cannot save valid log destination %q", value)
		}
	}
	for _, value := range []string{"apps/mysql/main", "apps/mysql/frps", "apps/supervisor/invalid", "apps/frp/0123456789abcdef"} {
		if validNavigation("oplogs", value) {
			t.Errorf("accepted unknown log destination %q", value)
		}
	}
}
