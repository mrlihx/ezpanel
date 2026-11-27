package docker

import (
	"strings"
	"testing"
)

func TestImagePullReference(t *testing.T) {
	const image = "ghcr.io/mrlihx/clash-config-manager"
	digest := image + "@sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct{ input, want string }{
		{image, image + ":latest"},
		{image + ":sha-abcdef", image + ":sha-abcdef"},
		{digest, digest},
		{"registry.example.com:5000/team/app", "registry.example.com:5000/team/app:latest"},
	} {
		if got := imagePullReference(tc.input); got != tc.want {
			t.Errorf("拉取引用 %q = %q，期望 %q", tc.input, got, tc.want)
		}
	}
}
