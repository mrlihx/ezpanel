package docker

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPruneImageScope(t *testing.T) {
	for _, tc := range []struct {
		query   string
		filters string
		status  int
	}{
		{"", `{"dangling":["true"]}`, http.StatusOK},
		{"?scope=dangling", `{"dangling":["true"]}`, http.StatusOK},
		{"?scope=unused", `{"dangling":["false"]}`, http.StatusOK},
		{"?scope=all", "", http.StatusBadRequest},
	} {
		t.Run(tc.query, func(t *testing.T) {
			called := false
			m := &Module{client: &http.Client{Transport: composeLogTestTransport(func(req *http.Request) (*http.Response, error) {
				called = true
				if req.Method != http.MethodPost || req.URL.Path != "/images/prune" || req.URL.Query().Get("filters") != tc.filters {
					t.Fatalf("意外的 Docker 请求 %s %s", req.Method, req.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"SpaceReclaimed":0}`))}, nil
			})}}
			router := gin.New()
			router.POST("/prune/:kind", m.prune)
			res := httptest.NewRecorder()
			router.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/prune/images"+tc.query, nil))
			if res.Code != tc.status || called != (tc.status == http.StatusOK) {
				t.Fatalf("响应状态 %d，是否请求 Docker %t，响应 %s", res.Code, called, res.Body.String())
			}
		})
	}
}
