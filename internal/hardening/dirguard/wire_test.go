package dirguard

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"github.com/cilium/ebpf"
	"testing"
)

//go:embed bpf/guard.o
var compiledObject []byte

func TestPolicyWireLayout(t *testing.T) {
	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(compiledObject))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := binary.Size(record{}), int(spec.Maps["template"].ValueSize); got != want {
		t.Fatalf("Go映射布局=%d,内核布局=%d", got, want)
	}
	if spec.Maps["scratch"].ValueSize < 4096 {
		t.Fatal("访问路径缓冲未覆盖PATH_MAX")
	}
	if len(spec.Programs) != 15 {
		t.Fatalf("内核挂钩数量=%d", len(spec.Programs))
	}
}
