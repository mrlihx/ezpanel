package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	dir := "dist"
	if len(os.Args) == 2 {
		dir = os.Args[1]
	}
	out := ""
	for _, name := range []string{"ezpanel-linux-amd64", "ezpanel-linux-arm64"} {
		f, e := os.Open(filepath.Join(dir, name))
		if e != nil {
			return e
		}
		hash := sha256.New()
		_, e = io.Copy(hash, f)
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		out += fmt.Sprintf("%x  %s\n", hash.Sum(nil), name)
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(out), 0644)
}
