package file

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"ezpanel/internal/response"

	"github.com/gin-gonic/gin"
	"golang.org/x/sys/unix"
)

func (m *Module) previewFile(c *gin.Context) {
	path, err := m.policy.Check(c.Query("path"), false)
	if err != nil {
		fail(c, err)
		return
	}
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		fail(c, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		fail(c, err)
		return
	}
	if !info.Mode().IsRegular() {
		response.Fail(c, response.Forbidden, "仅支持普通文件预览")
		return
	}
	var header [512]byte
	n, err := f.ReadAt(header[:], 0)
	if err != nil && err != io.EOF {
		fail(c, err)
		return
	}
	kind := previewMediaType(header[:n], strings.ToLower(filepath.Ext(info.Name())))
	if kind == "" {
		response.Fail(c, response.Forbidden, "文件格式不支持在线预览")
		return
	}
	c.Header("Content-Type", kind)
	c.Header("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": safeDownloadName(info.Name())}))
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cross-Origin-Resource-Policy", "same-origin")
	c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'")
	http.ServeContent(c.Writer, c.Request, info.Name(), info.ModTime(), contextReadSeeker{c.Request.Context(), f})
}

func previewMediaType(header []byte, ext string) string {
	// 按文件签名确认类型，不能借扩展名以内联方式提供 HTML 或 SVG。
	kind := http.DetectContentType(header)
	switch kind {
	case "application/pdf", "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "image/x-icon",
		"audio/mpeg", "audio/wave", "video/mp4":
		if kind == "video/mp4" && ext == ".m4a" {
			return "audio/mp4"
		}
		return kind
	case "application/ogg":
		if ext == ".ogv" {
			return "video/ogg"
		}
		return "audio/ogg"
	case "video/webm":
		if ext == ".weba" {
			return "audio/webm"
		}
		return kind
	}
	if bytes.HasPrefix(header, []byte("fLaC")) {
		return "audio/flac"
	}
	if ext == ".mp3" && len(header) >= 2 && header[0] == 0xff && header[1]&0xe0 == 0xe0 {
		return "audio/mpeg"
	}
	if len(header) >= 12 && string(header[4:8]) == "ftyp" {
		switch string(header[8:12]) {
		case "avif", "avis":
			return "image/avif"
		case "M4A ":
			return "audio/mp4"
		case "qt  ":
			return "video/quicktime"
		}
	}
	return ""
}
