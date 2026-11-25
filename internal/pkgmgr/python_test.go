package pkgmgr

import (
	"slices"
	"testing"
)

func TestRuntimeEnvironmentsReadWriteAndDuplicateVersions(t *testing.T) {
	for _, id := range []string{"python", "go", "nodejs"} {
		t.Run(id, func(t *testing.T) {
			dir := t.TempDir()
			got, err := readRuntimeEnvironments(dir)
			if err != nil || len(got) != 0 {
				t.Fatalf("empty dir items = %v, error = %v", got, err)
			}
			items := []RuntimeEnvironment{
				{ID: "first", Version: "3.12", Image: "sha256:img1"},
				{ID: "second", Version: "3.12", Image: "sha256:img1"},
			}
			if err := writeRuntimeEnvironments(dir, items); err != nil {
				t.Fatal(err)
			}
			got, err = readRuntimeEnvironments(dir)
			if err != nil || !slices.Equal(items, got) {
				t.Fatalf("stored items = %v, error = %v", got, err)
			}
			if err := writeRuntimeEnvironments(dir, []RuntimeEnvironment{}); err != nil {
				t.Fatal(err)
			}
			got, err = readRuntimeEnvironments(dir)
			if err != nil || len(got) != 0 {
				t.Fatalf("cleared items = %v, error = %v", got, err)
			}
		})
	}
}

func TestNewerContainerReleasePostgres(t *testing.T) {
	if !newerContainerRelease("postgresql", "18.8", "18.7") {
		t.Fatal("expected postgresql 18.8 to be newer than 18.7")
	}
	if newerContainerRelease("postgresql", "18.7", "18.7") {
		t.Fatal("expected equal postgresql versions not to be newer")
	}
	if newerContainerRelease("postgresql", "18.0", "17.9") {
		t.Fatal("expected cross-major postgresql version not to be treated as patch upgrade")
	}
	if !newerContainerRelease("postgresql", "9.6.24", "9.6.23") {
		t.Fatal("expected postgresql 9.6.24 to be newer than 9.6.23")
	}
}
