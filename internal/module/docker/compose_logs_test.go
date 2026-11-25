package docker

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type composeLogTestTransport func(*http.Request) (*http.Response, error)

func (transport composeLogTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestComposeProjectLogsReadEngineReplicasInOrder(t *testing.T) {
	client := &http.Client{Transport: composeLogTestTransport(func(req *http.Request) (*http.Response, error) {
		query := req.URL.Query()
		var body string
		switch req.URL.Path {
		case "/containers/json":
			var filters map[string][]string
			if err := json.Unmarshal([]byte(query.Get("filters")), &filters); err != nil || strings.Join(filters["label"], "|") != "com.docker.compose.project=demo|com.docker.compose.service=web" || query.Get("all") != "true" {
				t.Fatalf("unexpected filters: %v", query)
			}
			body = `[
				{"Id":"first","Names":["/demo-web-1"],"State":"exited","Labels":{"com.docker.compose.project":"demo","com.docker.compose.service":"web"}},
				{"Id":"second","Names":["/demo-web-2"],"State":"running","Labels":{"com.docker.compose.project":"demo","com.docker.compose.service":"web"}},
				{"Id":"foreign","Labels":{"com.docker.compose.project":"other","com.docker.compose.service":"web"}},
				{"Id":"db","Labels":{"com.docker.compose.project":"demo","com.docker.compose.service":"db"}}
			]`
		case "/containers/first/logs", "/containers/second/logs":
			if _, err := strconv.ParseInt(query.Get("since"), 10, 64); err != nil || query.Get("tail") != "100" || query.Get("timestamps") != "true" || query.Get("stdout") != "true" || query.Get("stderr") != "true" {
				t.Fatalf("unexpected log request: %v", query)
			}
			if req.URL.Path == "/containers/first/logs" {
				for _, line := range []string{"2026-05-01T00:00:03.000000000Z match newest\n", "2026-05-01T00:00:04.000000000Z ignored\n"} {
					var header [8]byte
					header[0] = 2
					binary.BigEndian.PutUint32(header[4:], uint32(len(line)))
					body += string(header[:]) + line
				}
			} else {
				body = "2026-05-01T00:00:01.000000000Z match oldest\n"
			}
		default:
			t.Fatalf("unexpected Docker route: %s", req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	m := &Module{client: client}
	router := gin.New()
	router.GET("/projects/:name/logs", m.composeProjectLogs)

	for _, download := range []bool{false, true} {
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequest("GET", "/projects/demo/logs?service=web&since=10m&keyword=match&download="+strconv.FormatBool(download), nil))
		if res.Code != http.StatusOK {
			t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
		}
		want := "demo-web-2 | match oldest\ndemo-web-1 | match newest"
		if download {
			if res.Body.String() != want || res.Header().Get("Content-Disposition") != `attachment; filename="demo-web.log"` {
				t.Fatalf("unexpected download: %s %q", res.Header(), res.Body.String())
			}
			continue
		}
		var out struct{ Data struct{ Text string } }
		if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil || out.Data.Text != want {
			t.Fatalf("unexpected response: %s", res.Body.String())
		}
	}
}

func TestFollowComposeLogsDecodesFramesAndKeepsReadingAfterLongLine(t *testing.T) {
	dockerClient := &http.Client{Transport: composeLogTestTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("follow") != "true" {
			t.Fatal("Docker 请求缺少 follow 参数")
		}
		var body strings.Builder
		for _, frame := range []string{
			"2026-05-01T00:00:00Z matching " + strings.Repeat("x", 16<<10) + "\n",
			"2026-05-01T00:00:01Z skip this\n",
			"2026-05-01T00:00:02Z match across",
			" frames\n",
		} {
			var header [8]byte
			header[0] = 1
			binary.BigEndian.PutUint32(header[4:], uint32(len(frame)))
			body.Write(header[:])
			body.WriteString(frame)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/vnd.docker.multiplexed-stream"}},
			Body:       io.NopCloser(strings.NewReader(body.String())),
		}, nil
	})}
	module := &Module{client: dockerClient}
	var lines []composeLiveLog
	err := module.followComposeContainerLogs(context.Background(), composeContainer{ID: "web", Names: []string{"/demo-web-1"}}, "follow=true", composeLogFilters{keyword: "match"}, func(line composeLiveLog) {
		lines = append(lines, line)
	})
	if err != io.EOF {
		t.Fatalf("stream ended with %v", err)
	}
	if len(lines) != 2 || !lines[0].truncated || lines[1].truncated || lines[1].text != "demo-web-1 | match across frames" {
		t.Fatalf("unexpected log sequence: %d lines", len(lines))
	}
	if !lines[0].time.Before(lines[1].time) {
		t.Fatal("Docker 时间戳未保留")
	}
}
