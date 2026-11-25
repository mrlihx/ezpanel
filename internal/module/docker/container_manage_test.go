package docker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
)

func TestRecreateContainerReusesAnonymousVolumes(t *testing.T) {
	cfg := config.Config{InstallDir: t.TempDir()}
	if err := os.MkdirAll(cfg.DataDir(), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(cfg.DataDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close()
	tasks, err := task.New(context.Background(), db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer tasks.Close()

	type mount struct {
		Source, Target string
		VolumeOptions  struct{ NoCopy bool }
	}
	type createRequest struct {
		HostConfig struct {
			Mounts []mount
			Binds  []string
		}
	}
	created := make(chan createRequest, 1)
	m := &Module{tasks: tasks, auth: &auth.Service{}, client: &http.Client{Transport: composeLogTestTransport(func(req *http.Request) (*http.Response, error) {
		body := "{}"
		switch req.Method + " " + req.URL.Path {
		case "GET /containers/demo/json":
			body = `{"Id":"0123456789ab","Name":"/demo","Image":"sha256:old",
				"HostConfig":{"Mounts":[{"Type":"volume","Target":"/data/","VolumeOptions":{"NoCopy":true}}],"Binds":["/cache"]},
				"Mounts":[{"Type":"volume","Name":"existing-data","Destination":"/data","RW":true},
					{"Type":"volume","Name":"existing-cache","Destination":"/cache","RW":true},
					{"Type":"volume","Name":"existing-image-volume","Destination":"/image-data","RW":true}]}`
		case "GET /images/sha256:old/json":
			body = `{"Id":"sha256:old"}`
		case "POST /containers/create":
			var payload createRequest
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				return nil, err
			}
			created <- payload
			body = `{"Id":"replacement"}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	r := gin.New()
	r.POST("/containers/:id/recreate", m.recreateContainer)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/containers/demo/recreate", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("recreate returned %d: %s", w.Code, w.Body.String())
	}
	select {
	case payload := <-created:
		mounts, binds := payload.HostConfig.Mounts, payload.HostConfig.Binds
		if len(mounts) != 2 || mounts[0].Source != "existing-data" || !mounts[0].VolumeOptions.NoCopy || mounts[1].Source != "existing-image-volume" ||
			len(binds) != 1 || binds[0] != "existing-cache:/cache" {
			t.Fatalf("recreate did not preserve existing volumes and options: mounts=%+v binds=%v", mounts, binds)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Docker did not receive the create request")
	}
}

func TestContainerMaintenanceRespectsOwnership(t *testing.T) {
	cfg := config.Config{InstallDir: t.TempDir()}
	if err := os.MkdirAll(cfg.DataDir(), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(cfg.DataDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close()
	tasks, err := task.New(context.Background(), db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer tasks.Close()

	calls := []string{}
	m := &Module{tasks: tasks, client: &http.Client{Transport: composeLogTestTransport(func(req *http.Request) (*http.Response, error) {
		key := req.Method + " " + req.URL.Path
		calls = append(calls, key)
		var body string
		switch key {
		case "GET /containers/json":
			body = `[{"Id":"app","State":"exited","Labels":{"ezpanel.managed":"true"}},{"Id":"compose","State":"exited","Labels":{"com.docker.compose.project":"demo"}},{"Id":"standalone","State":"exited"},{"Id":"active","State":"running"}]`
		case "DELETE /containers/standalone":
			if req.URL.Query().Get("v") != "false" || req.URL.Query().Has("force") {
				t.Fatal(req.URL)
			}
		case "GET /containers/app/json":
			body = `{"Config":{"Labels":{"ezpanel.managed":"true"}},"HostConfig":{"Memory":268435456,"MemorySwap":536870912,"CpuQuota":50000,"CpuPeriod":100000}}`
		case "POST /containers/app/update":
			var data struct {
				Memory, MemorySwap, CpuQuota int64
				RestartPolicy                struct{ Name string }
			}
			if err := json.NewDecoder(req.Body).Decode(&data); err != nil ||
				data.Memory != 1024<<20 || data.MemorySwap != 1280<<20 ||
				data.CpuQuota != 150000 || data.RestartPolicy.Name != "always" {
				t.Fatalf("unexpected hot update: %+v (%v)", data, err)
			}
			body = `{"Warnings":[]}`
		default:
			t.Fatalf("unexpected Docker request %s", key)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	r := gin.New()
	r.POST("/prune/:kind", m.prune)
	r.POST("/containers/:id/update", m.updateContainer)
	r.POST("/containers/:id/action", m.containerAction)
	for _, sample := range []struct {
		url, body string
		code      int
	}{
		{"/prune/containers", "", 200},
		{"/containers/app/update", `{"memoryMB":1024,"cpus":1.5,"restart":"always"}`, 200},
		{"/containers/app/action", `{"action":"rename","name":"other"}`, 409},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, sample.url, strings.NewReader(sample.body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != sample.code {
			t.Fatalf("%s returned %d: %s", sample.url, w.Code, w.Body.String())
		}
	}
	if len(calls) != 5 {
		t.Fatal(calls)
	}
}
