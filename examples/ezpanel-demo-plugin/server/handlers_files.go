package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mrlihx/ezpanel/sdk/go/panelsdk"
)

// registerFileRoutes 注册受控文件接口（FSList / FSStat / FSHash / FSRead / FSWrite / FSMkdir / FSDelete / FSOpen / FSUpload）。
// 安全边界：所有路径均由 Host 按 manifest.fsRoots（本插件为 /tmp）与 pathsafe.Policy 校验；
// 文本读写限 UTF-8 且 <= 2MiB，二进制流式上传（FSUpload）上限 100MiB（本示例限制 10MiB）。
func registerFileRoutes(p *panelsdk.Plugin) error {
	if err := p.HandleFunc("/api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		path := r.URL.Query().Get("path")
		if path == "" {
			path = "/tmp"
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if offset < 0 {
			offset = 0
		}
		page, err := p.Host.FSList(r.Context(), path, offset)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, page)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/fs/stat", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		path := r.URL.Query().Get("path")
		if path == "" {
			fail(w, errors.New("缺少文件路径"))
			return
		}
		meta, err := p.Host.FSStat(r.Context(), path)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, meta)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/fs/hash", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		path := r.URL.Query().Get("path")
		if path == "" {
			fail(w, errors.New("缺少文件路径"))
			return
		}
		digest, err := p.Host.FSHash(r.Context(), path)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"path": path, "sha256": digest})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/fs/read", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		path := r.URL.Query().Get("path")
		if path == "" {
			fail(w, errors.New("缺少文件路径"))
			return
		}
		content, err := p.Host.FSRead(r.Context(), path)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"path": path, "content": content})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/fs/write", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			Mode    string `json:"mode"`
		}
		if !decodeJSON(w, r, 3<<20, &in) {
			return
		}
		if strings.TrimSpace(in.Path) == "" {
			fail(w, errors.New("缺少目标路径"))
			return
		}
		if in.Mode == "" {
			in.Mode = "0600"
		}
		if err := p.Host.FSWrite(r.Context(), in.Path, in.Content, in.Mode); err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"path": in.Path})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/fs/mkdir", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			Path string `json:"path"`
		}
		if !decodeJSON(w, r, 4096, &in) {
			return
		}
		if err := p.Host.FSMkdir(r.Context(), in.Path); err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"path": in.Path})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/fs/delete", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			Path string `json:"path"`
		}
		if !decodeJSON(w, r, 4096, &in) {
			return
		}
		if err := p.Host.FSDelete(r.Context(), in.Path); err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"deleted": in.Path})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/fs/upload", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		path := r.URL.Query().Get("path")
		if strings.TrimSpace(path) == "" {
			fail(w, errors.New("请指定上传目标路径"))
			return
		}
		limited := http.MaxBytesReader(w, r.Body, 10<<20)
		if err := p.Host.FSUpload(r.Context(), path, limited); err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"path": path})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/fs/stream-preview", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		path := r.URL.Query().Get("path")
		if strings.TrimSpace(path) == "" {
			fail(w, errors.New("缺少文件路径"))
			return
		}
		rc, err := p.Host.FSOpen(r.Context(), path)
		if err != nil {
			fail(w, err)
			return
		}
		defer rc.Close()

		hash := sha256.New()
		var total int64
		preview := make([]byte, 0, 4096)
		buf := make([]byte, 32<<10)
		for {
			n, readErr := rc.Read(buf)
			if n > 0 {
				total += int64(n)
				_, _ = hash.Write(buf[:n])
				if len(preview) < 4096 {
					take := min(4096-len(preview), n)
					preview = append(preview, buf[:take]...)
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				fail(w, readErr)
				return
			}
		}
		isText := utf8.Valid(preview)
		textPreview := ""
		if isText {
			textPreview = string(preview)
		}
		ok(w, map[string]any{
			"path":        path,
			"bytes":       total,
			"sha256":      hex.EncodeToString(hash.Sum(nil)),
			"isUtf8":      isText,
			"textPreview": textPreview,
			"hexHead":     hex.EncodeToString(preview[:min(64, len(preview))]),
		})
	}); err != nil {
		return err
	}

	return nil
}
