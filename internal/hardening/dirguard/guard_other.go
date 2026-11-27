//go:build !linux

package dirguard

import "errors"

type Guard struct{}

func New(string) (*Guard, error) { return &Guard{}, nil }
func (*Guard) Supported() (bool, string) {
	return false, "目录实时保护仅支持启用 BPF LSM 的 Linux 内核"
}
func (*Guard) Sync(policies []Policy) error {
	if len(policies) > 0 {
		return errors.New("目录实时保护仅支持启用 BPF LSM 的 Linux 内核")
	}
	return nil
}
func (*Guard) Close() error { return nil }
