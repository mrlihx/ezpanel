package file

import (
	"errors"
	"ezpanel/internal/response"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (m *Module) upload(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<30)
	reader, e := c.Request.MultipartReader()
	if e != nil {
		response.Fail(c, response.Invalid, "上传格式无效")
		return
	}
	dir := ""
	overwrite := false
	count := 0
	result := task.BatchResult{Results: []task.ItemResult{}}
	for {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			var max *http.MaxBytesError
			if errors.As(e, &max) {
				fail(c, errLimit)
			} else {
				response.Fail(c, response.Invalid, "上传中断或格式无效")
			}
			return
		}
		field := part.FormName()
		if field == "dir" || field == "overwrite" {
			if count > 0 {
				response.Fail(c, response.Invalid, "上传参数必须位于文件之前")
				return
			}
			b, e := io.ReadAll(io.LimitReader(part, 8193))
			part.Close()
			if e != nil || len(b) > 8192 {
				response.Fail(c, response.Invalid, "上传参数无效")
				return
			}
			if field == "dir" {
				dir, e = m.policy.Check(string(b), true)
				if e != nil {
					fail(c, e)
					return
				}
			} else {
				if string(b) != "true" && string(b) != "false" {
					response.Fail(c, response.Invalid, "覆盖参数无效")
					return
				}
				overwrite = string(b) == "true"
			}
			continue
		}
		if (field != "files[]" && field != "dirs[]") || dir == "" {
			response.Fail(c, response.Invalid, "请先提供上传目录与文件")
			return
		}
		count++
		if count > 100 {
			fail(c, errLimit)
			return
		}
		_, params, e := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		name := params["filename"]
		isDir := field == "dirs[]"
		if isDir {
			var value []byte
			value, e = io.ReadAll(io.LimitReader(part, 8193))
			part.Close()
			name = string(value)
		}
		parts := uploadPathParts(name)
		if e != nil || parts == nil {
			response.Fail(c, response.Invalid, "文件相对路径无效")
			return
		}
		dst, e := m.policy.CheckLink(filepath.Join(dir, name), true)
		if e != nil {
			fail(c, e)
			return
		}
		dirs := parts[:len(parts)-1]
		if isDir {
			dirs = parts
		}
		e = makeUploadDirectories(dir, dirs)
		if e == nil && !isDir {
			e = m.atomic(c.Request.Context(), dst, part, overwrite, 2<<30, 0755)
		}
		part.Close()
		code, msg := errorResult(e)
		result.Results = append(result.Results, task.ItemResult{Target: name, Code: code, Msg: msg})
		if e != nil {
			result.FailedCount++
			var max *http.MaxBytesError
			if errors.As(e, &max) || strings.Contains(e.Error(), "request body too large") {
				fail(c, errLimit)
				return
			}
		} else {
			result.SuccessCount++
		}
	}
	if count == 0 {
		response.Fail(c, response.Invalid, "请选择上传文件")
		return
	}
	response.OK(c, result)
}

func uploadPathParts(name string) []string {
	if len(name) > 8192 {
		return nil
	}
	parts := strings.Split(name, "/")
	for _, part := range parts {
		if !nameValid(part) {
			return nil
		}
	}
	return parts
}

func makeUploadDirectories(dir string, parts []string) error {
	for _, name := range parts {
		dir = filepath.Join(dir, name)
		e := os.Mkdir(dir, 0755)
		if e == nil {
			e = os.Chmod(dir, 0755)
			inheritParentOwner(dir)
		} else if os.IsExist(e) {
			info, err := os.Lstat(dir)
			e = err
			if e == nil && !info.IsDir() {
				e = errors.New("上传目录已存在同名文件或链接")
			}
		}
		if e != nil {
			return e
		}
	}
	return nil
}
