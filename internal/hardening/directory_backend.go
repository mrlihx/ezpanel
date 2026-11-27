package hardening

import (
	"errors"
	"ezpanel/internal/hardening/dirguard"
)

type unavailableDirectoryBackend struct{ reason string }

func (b unavailableDirectoryBackend) Supported() (bool, string) { return false, b.reason }
func (b unavailableDirectoryBackend) Sync(policies []DirectoryPolicy) error {
	if len(policies) == 0 {
		return nil
	}
	return errors.New(b.reason)
}

type kernelDirectoryBackend struct{ guard *dirguard.Guard }

func (b kernelDirectoryBackend) Supported() (bool, string) { return b.guard.Supported() }
func (b kernelDirectoryBackend) Sync(policies []DirectoryPolicy) error {
	entries := make([]dirguard.Policy, len(policies))
	for i, rule := range policies {
		entries[i] = dirguard.Policy{ID: rule.ID, Path: rule.Path, Exclusions: rule.Exclusions}
	}
	return b.guard.Sync(entries)
}
func newDirectoryBackend(dataDir string) DirectoryBackend {
	guard, err := dirguard.New(dataDir)
	if err != nil {
		return unavailableDirectoryBackend{err.Error()}
	}
	return kernelDirectoryBackend{guard}
}
