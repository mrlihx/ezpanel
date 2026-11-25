package pkgmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

const containerCatalogSource = "docker-hub"

var containerPatchTag = regexp.MustCompile(`^([0-9]+\.[0-9]+\.[0-9]+)$`)
var phpFPMTag = regexp.MustCompile(`^([0-9]+\.[0-9]+\.[0-9]+)-fpm$`)
var openRestyPatchTag = regexp.MustCompile(`^([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+)-[0-9]+-alpine$`)
var postgresAlpineTag = regexp.MustCompile(`^([0-9]+\.[0-9]+(?:\.[0-9]+)?)-alpine$`)

func containerBranch(id, version string) string {
	if id == "postgresql" {
		parts := strings.Split(version, ".")
		if len(parts) >= 2 && parts[0] != "9" {
			return parts[0]
		}
	}
	return series(version)
}

func containerTagPattern(id string) *regexp.Regexp {
	switch {
	case IsPHP(id):
		return phpFPMTag
	case id == "openresty":
		return openRestyPatchTag
	case id == "postgresql":
		return postgresAlpineTag
	default:
		return containerPatchTag
	}
}

func (m *Manager) refreshFromMirrors(ctx context.Context, id, image, namespace, repository string, best map[string]Release) bool {
	mirrors := m.DockerMirrors()
	if len(mirrors) == 0 {
		return false
	}
	pattern := containerTagPattern(id)
	for _, mirror := range mirrors {
		address := strings.TrimRight(mirror, "/") + "/v2/" + namespace + "/" + repository + "/tags/list"
		probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		data, err := m.sourceRequest(probeCtx, address, 8<<20)
		cancel()
		if err != nil {
			continue
		}
		var payload struct {
			Tags []string `json:"tags"`
		}
		if err := json.Unmarshal(data, &payload); err != nil || len(payload.Tags) == 0 {
			continue
		}
		for _, tagName := range payload.Tags {
			match := pattern.FindStringSubmatch(tagName)
			if match == nil {
				continue
			}
			ver := match[1]
			branch := containerBranch(id, ver)
			candidate, exists := best[branch]
			if !exists {
				continue
			}
			if newerContainerRelease(id, ver, candidate.Version) {
				candidate.Version, candidate.URL = ver, image+":"+tagName
				best[branch] = candidate
			}
		}
		return true
	}
	return false
}

func (m *Manager) containerVersionInfo(id string) versionCache {
	key := id + ":" + containerCatalogSource
	m.sourcesMu.Lock()
	defer m.sourcesMu.Unlock()
	cache, loaded := m.sourceCache[key]
	if !loaded {
		if name := m.cachePath(id, repository{key: containerCatalogSource}); name != "" {
			data, _ := os.ReadFile(name)
			_ = json.Unmarshal(data, &cache)
		}
		m.sourceCache[key] = cache
	}
	return cache
}

func (m *Manager) refreshContainerVersions(ctx context.Context, id string, releases []Release) error {
	best := make(map[string]Release, len(releases))
	for _, release := range append(slices.Clone(releases), m.containerVersionInfo(id).List...) {
		branch := containerBranch(id, release.Version)
		if current, exists := best[branch]; !exists || newerContainerRelease(id, release.Version, current.Version) {
			best[branch] = release
		}
	}
	var sampleImage, sampleNS, sampleRepo string
	for _, candidate := range best {
		sampleImage, _, _ = strings.Cut(candidate.URL, ":")
		var hasNS bool
		sampleNS, sampleRepo, hasNS = strings.Cut(sampleImage, "/")
		if !hasNS {
			sampleNS, sampleRepo = "library", sampleImage
		}
		break
	}
	if sampleRepo == "" || !m.refreshFromMirrors(ctx, id, sampleImage, sampleNS, sampleRepo, best) {
		pattern := containerTagPattern(id)
		for branch, candidate := range best {
			image, _, _ := strings.Cut(candidate.URL, ":")
			namespace, repository, hasNamespace := strings.Cut(image, "/")
			if !hasNamespace {
				namespace, repository = "library", image
			}
			address := "https://hub.docker.com/v2/namespaces/" + namespace + "/repositories/" + repository + "/tags?page_size=100&name=" + url.QueryEscape(branch+".")
			data, err := m.sourceRequest(ctx, address, 8<<20)
			if err != nil {
				return err
			}
			var page struct {
				Results []struct {
					Name   string
					Images []struct{ OS, Architecture string }
				}
			}
			if err := json.Unmarshal(data, &page); err != nil {
				return err
			}
			architecture := runtime.GOARCH
			if candidate.Platform != "" {
				_, architecture, _ = strings.Cut(candidate.Platform, "/")
			}
			for _, tag := range page.Results {
				match := pattern.FindStringSubmatch(tag.Name)
				if match == nil || !newerContainerRelease(id, match[1], candidate.Version) {
					continue
				}
				if slices.ContainsFunc(tag.Images, func(image struct{ OS, Architecture string }) bool {
					return image.OS == "linux" && image.Architecture == architecture
				}) {
					candidate.Version, candidate.URL = match[1], image+":"+tag.Name
				}
			}
			best[branch] = candidate
		}
	}
	cache := versionCache{SyncedAt: time.Now()}
	for _, release := range releases {
		cache.List = append(cache.List, best[containerBranch(id, release.Version)])
	}

	m.sourcesMu.Lock()
	defer m.sourcesMu.Unlock()
	if name := m.cachePath(id, repository{key: containerCatalogSource}); name != "" {
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			return err
		}
		payload, err := json.Marshal(cache)
		if err != nil {
			return err
		}
		if err = os.WriteFile(name, payload, 0600); err != nil {
			return err
		}
	}
	m.sourceCache[id+":"+containerCatalogSource] = cache
	return nil
}

func (m *Manager) refreshLock(id string) *sync.Mutex {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	if m.refreshLocks == nil {
		m.refreshLocks = map[string]*sync.Mutex{}
	}
	lock, ok := m.refreshLocks[id]
	if !ok {
		lock = &sync.Mutex{}
		m.refreshLocks[id] = lock
	}
	return lock
}

// RefreshContainerIfEmpty 在容器应用版本缓存为空时按需静默拉取一次最新补丁版本，
// 避免全新安装时选到内置静态旧版；失败或超时时静默降级，不阻塞安装流程。
func (m *Manager) RefreshContainerIfEmpty(ctx context.Context, id string) {
	if !UsesDocker(id) || IsProjectRuntime(id) {
		return
	}
	if !m.containerVersionInfo(id).SyncedAt.IsZero() {
		return
	}
	lock := m.refreshLock(id)
	lock.Lock()
	defer lock.Unlock()
	if !m.containerVersionInfo(id).SyncedAt.IsZero() {
		return
	}
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	_ = m.refreshContainerVersions(reqCtx, id, containerReleases(id))
}

func (m *Manager) publishedContainerReleases(ctx context.Context, id string, refresh bool) ([]Release, error) {
	rows := containerReleases(id)
	if IsProjectRuntime(id) {
		return rows, nil
	}
	if refresh {
		lock := m.refreshLock(id)
		lock.Lock()
		err := m.refreshContainerVersions(ctx, id, rows)
		lock.Unlock()
		if err != nil {
			return nil, fmt.Errorf("查询 Docker Hub 的 %s 镜像失败：%w", Name(id), err)
		}
	}
	cache := m.containerVersionInfo(id).List
	out := make([]Release, 0, len(rows))
	for _, row := range rows {
		latest := row
		for _, release := range cache {
			if newerContainerRelease(id, release.Version, latest.Version) {
				latest = release
			}
		}
		out = append(out, latest)
	}
	return out, nil
}

func (m *Manager) containerUpdateVersion(ctx context.Context, id, installed string) string {
	releases, _ := m.publishedContainerReleases(ctx, id, false)
	for _, release := range releases {
		if newerContainerRelease(id, release.Version, installed) {
			return release.Version
		}
	}
	return ""
}

// StartDailyUpdateCheck 面板启动后快速预热未缓存的容器应用版本，此后每天定时串行检查更新，避免触发镜像源限流。
func (m *Manager) StartDailyUpdateCheck(ctx context.Context) {
	runOnce := func(interval time.Duration) {
		states := m.Catalog(ctx)
		for _, state := range states {
			if !state.Updateable {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			if info := m.VersionInfo(state.ID); !info.SyncedAt.IsZero() && time.Since(info.SyncedAt) < 20*time.Hour {
				continue
			}
			reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			_, _ = m.Releases(reqCtx, state.ID, true)
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
		}
	}

	select {
	case <-ctx.Done():
		return
	case <-time.After(10 * time.Second):
	}
	runOnce(2 * time.Second)

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce(5 * time.Second)
		}
	}
}
