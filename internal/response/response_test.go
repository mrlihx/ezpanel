package response

import (
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBind(t *testing.T) {
	for _, body := range []string{`{"bad":1}`, `{} {}`, strings.Repeat(" ", 4<<20) + `{}`} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
		var dst struct {
			Name string `json:"name"`
		}
		if Bind(c, &dst) {
			t.Fatal("accepted invalid body")
		}
		if w.Code < 400 {
			t.Fatal(w.Code)
		}
	}
}
func TestSummary(t *testing.T) {
	s := "line\n\\" + strings.Repeat("你", 1000)
	got := Summary(s)
	if !strings.HasPrefix(got, "line\n\\") || len(got) > 2048 {
		t.Fatal("summary discarded", len(got))
	}
}
func TestTrailingOversize(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{}`+strings.Repeat(" ", 4<<20)))
	var v map[string]any
	if Bind(c, &v) || w.Code != 413 {
		t.Fatal(w.Code)
	}
}
