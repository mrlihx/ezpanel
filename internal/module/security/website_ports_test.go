package security

import (
	"context"
	"errors"
	"testing"

	"ezpanel/internal/config"
)

func TestWebsitePortsSaveAndReuse(t *testing.T) {
	module := New(config.Config{InstallDir: t.TempDir()}, nil)
	ctx := context.Background()
	if err := module.WithWebsitePorts(ctx, []int{80, 8080}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := module.WithWebsitePorts(ctx, []int{80, 8080}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	failed := errors.New("website save failed")
	if err := module.WithWebsitePorts(ctx, []int{443}, func() error { return failed }); !errors.Is(err, failed) {
		t.Fatalf("got %v, want %v", err, failed)
	}
	state, err := module.readFirewall()
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || len(state.Rules) != 2 || state.Rules[0].Ports != "80" || state.Rules[1].Ports != "8080" {
		t.Fatalf("unexpected saved firewall state: %+v", state)
	}
	if state.Rules[0].Origin != "website" || state.Rules[0].Remark != "" {
		t.Fatalf("自动放行应标记来源，备注默认留空：%+v", state.Rules[0])
	}
}
