package pkgmgr

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"ezpanel/internal/executor"
)

func TestContainerMirrorReleases(t *testing.T) {
	manager := New()
	choice, err := manager.SelectSource("mysql", SourceSelection{Source: "custom", Mirror: " https://Mirror.example.com:5443/dockerhub/ "})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{
		"mysql":     "mirror.example.com:5443/dockerhub/library/mysql:8.4.13",
		"php-8.5":   "mirror.example.com:5443/dockerhub/library/php:8.5.12-fpm",
		"nodejs":    "mirror.example.com:5443/dockerhub/library/node:24.22.3-bookworm-slim",
		"openresty": "mirror.example.com:5443/dockerhub/openresty/openresty:1.31.8.1-0-alpine",
	} {
		releases, err := manager.Releases(context.Background(), id, false, choice)
		if err != nil {
			t.Fatal(err)
		}
		if len(releases) == 0 || releases[0].URL != want {
			t.Errorf("%s releases = %+v, want first image %s", id, releases, want)
		}
	}
	if _, err := manager.SelectSource("mysql", SourceSelection{Source: "custom", Mirror: "https://mirror.example.com?token=a"}); err == nil {
		t.Fatal("mirror with a query was accepted")
	}
}

func TestContainerRegistryReleases(t *testing.T) {
	manager := New()
	choice, err := manager.SelectSource("nodejs", SourceSelection{Source: "registry", Mirror: "registry.example.com:5000/hub"})
	if err != nil {
		t.Fatal(err)
	}
	releases, err := manager.Releases(context.Background(), "nodejs", false, choice)
	if err != nil || len(releases) == 0 {
		t.Fatalf("releases = %v, error = %v", releases, err)
	}
	if got := releases[0].URL; got != "registry.example.com:5000/hub/library/node:24.22.3-bookworm-slim" {
		t.Errorf("image = %q", got)
	}
}

func TestPullContainerRetriesMissingAttestation(t *testing.T) {
	const reference = "mirror.example.com:5443/dockerhub/library/php:8.2.35-fpm"
	const platform = "linux/arm64"
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const resolved = "mirror.example.com:5443/dockerhub/library/php@" + digest
	manager := New()
	manager.LookPath = func(string) (string, error) { return "/usr/bin/docker", nil }
	manager.Run = func(_ context.Context, _ time.Duration, _ []string, _ string, args ...string) (executor.Result, error) {
		if !slices.Equal(args, []string{"buildx", "imagetools", "inspect", "--raw", reference}) {
			t.Fatalf("unexpected command: %v", args)
		}
		return executor.Result{Stdout: `{"manifests":[
			{"digest":"sha256:attestation","platform":{"os":"unknown","architecture":"unknown"}},
			{"digest":"sha256:amd64","platform":{"os":"linux","architecture":"amd64"}},
			{"digest":"` + digest + `","platform":{"os":"linux","architecture":"arm64","variant":"v8"}}
		]}`}, nil
	}
	var attempts []string
	manager.Stream = func(_ context.Context, _ time.Duration, _ []string, log func(string), _ string, args ...string) (executor.Result, error) {
		if len(args) != 4 || !slices.Equal(args[:3], []string{"pull", "--platform", platform}) {
			t.Fatalf("unexpected command: %v", args)
		}
		attempts = append(attempts, args[3])
		if args[3] == reference {
			log("failed open: could not fetch content descriptor (application/vnd.in-toto+json) from remote: not found")
			return executor.Result{}, errors.New("exit status 1")
		}
		return executor.Result{}, nil
	}
	got, err := manager.pullContainer(context.Background(), reference, platform, func(line string) { t.Log(line) })
	if err != nil || got != resolved || !slices.Equal(attempts, []string{reference, resolved}) {
		t.Fatalf("pull returned %q, %v (attempts: %v)", got, err, attempts)
	}
}
