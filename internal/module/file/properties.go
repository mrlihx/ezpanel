package file

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"ezpanel/internal/auth"
	"ezpanel/internal/executor"
	"ezpanel/internal/response"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"golang.org/x/sys/unix"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type fileNote struct {
	Path   string `gorm:"primaryKey"`
	Remark string
}

func (fileNote) TableName() string { return "file_notes" }

func (m *Module) loadRemarks(rows []Entry) error {
	if len(rows) == 0 {
		return nil
	}
	paths := make([]string, len(rows))
	for i, row := range rows {
		paths[i] = row.Path
	}
	var notes []fileNote
	if err := m.db.Where("path IN ?", paths).Find(&notes).Error; err != nil {
		return err
	}
	byPath := make(map[string]string, len(notes))
	for _, note := range notes {
		byPath[note.Path] = note.Remark
	}
	for i := range rows {
		rows[i].Remark = byPath[rows[i].Path]
		if rows[i].IsDir || rows[i].LinkIsDir {
			if remark, ok := builtinRemarks[rows[i].Path]; ok {
				rows[i].Remark = remark
				rows[i].RemarkReadonly = true
			}
		}
	}
	return nil
}
func (m *Module) saveRemark(c *gin.Context) {
	var input struct {
		Path   string `json:"path"`
		Remark string `json:"remark"`
	}
	if !response.Bind(c, &input) {
		return
	}
	if !utf8.ValidString(input.Remark) || utf8.RuneCountInString(input.Remark) > 500 || strings.ContainsRune(input.Remark, 0) {
		response.Fail(c, response.Invalid, "备注最多500字，不能包含空字符")
		return
	}
	path, err := m.policy.CheckLink(input.Path, false)
	if err != nil {
		fail(c, err)
		return
	}
	if _, readonly := builtinRemarks[path]; readonly {
		response.Fail(c, response.Forbidden, "系统内置目录备注不能修改")
		return
	}
	if _, err = os.Lstat(path); err != nil {
		fail(c, err)
		return
	}
	note := fileNote{Path: path, Remark: strings.TrimSpace(input.Remark)}
	if note.Remark == "" {
		err = m.db.Where("path = ?", path).Delete(&fileNote{}).Error
	} else {
		err = m.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "path"}}, DoUpdates: clause.AssignmentColumns([]string{"remark"})}).Create(&note).Error
	}
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"remark": note.Remark})
}

// 目录改名或移动后同步其下备注；复制不改动源文件备注。
func (m *Module) moveRemarks(source, destination string) error {
	prefix := source + string(filepath.Separator)
	return m.db.Transaction(func(tx *gorm.DB) error {
		var notes []fileNote
		if err := tx.Where("path = ? OR substr(path,1,?) = ?", source, utf8.RuneCountInString(prefix), prefix).Find(&notes).Error; err != nil {
			return err
		}
		for _, note := range notes {
			if err := tx.Where("path = ?", note.Path).Delete(&fileNote{}).Error; err != nil {
				return err
			}
			note.Path = destination + strings.TrimPrefix(note.Path, source)
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "path"}}, DoUpdates: clause.AssignmentColumns([]string{"remark"})}).Create(&note).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (m *Module) properties(c *gin.Context) {
	path, err := m.policy.CheckLink(c.Query("path"), false)
	if err != nil {
		fail(c, err)
		return
	}
	info, err := os.Lstat(path)
	if err != nil {
		fail(c, err)
		return
	}
	out := statMetadata(info)
	out["entry"] = m.entry(path, info)
	kind := strings.TrimPrefix(filepath.Ext(info.Name()), ".")
	if info.IsDir() {
		kind = "文件夹"
	} else if info.Mode()&os.ModeSymlink != 0 {
		kind = "符号链接"
	} else if kind == "" {
		kind = "文件"
	}
	out["type"] = kind
	if info.Mode()&os.ModeSymlink != 0 {
		out["linkTarget"], _ = os.Readlink(path)
	}
	if c.Query("details") == "true" {
		out["attributes"] = "—"
		if command, err := executor.LookPath("lsattr"); err == nil {
			result, err := executor.Run(c.Request.Context(), 5*time.Second, command, "-d", "--", path)
			fields := strings.Fields(result.Stdout)
			if err == nil && len(fields) > 0 {
				out["attributes"] = fields[0]
			}
		}
		if info.Mode().IsRegular() {
			// 摘要只在打开详细信息时计算，目录列表不扫描文件内容。
			f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
			if err != nil {
				fail(c, err)
				return
			}
			defer f.Close()
			ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
			defer cancel()
			md5Hash, sha1Hash := md5.New(), sha1.New()
			if _, err = io.Copy(io.MultiWriter(md5Hash, sha1Hash), contextReader{ctx: ctx, r: f}); err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					response.Fail(c, response.System, "文件摘要计算超过30秒，请下载后计算")
				} else {
					fail(c, err)
				}
				return
			}
			out["md5"] = hex.EncodeToString(md5Hash.Sum(nil))
			out["sha1"] = hex.EncodeToString(sha1Hash.Sum(nil))
		}
	}
	response.OK(c, out)
}
func (m *Module) directorySize(c *gin.Context) {
	path := c.Query("path")
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		response.Fail(c, response.Invalid, "路径必须为绝对路径")
		return
	}
	path = filepath.Clean(path)
	info, err := os.Lstat(path)
	if err != nil {
		fail(c, err)
		return
	}
	if !info.IsDir() {
		response.Fail(c, response.Invalid, "请选择文件夹，符号链接不展开")
		return
	}
	if c.Request.Method == http.MethodPost {
		m.submitDirectorySize(c, path)
		return
	}
	// 这里只统计元数据，允许受保护目录；不改变内容读取和写入的路径限制。
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	size, err := treeSize(ctx, path)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			response.Fail(c, response.System, "目录较大，30秒内未完成计算，请缩小目录后重试")
		} else {
			fail(c, err)
		}
		return
	}
	response.OK(c, gin.H{"size": size})
}

func (m *Module) submitDirectorySize(c *gin.Context, path string) {
	session := auth.Current(c)
	ip := ""
	if m.auth != nil {
		ip = m.auth.ClientIP(c)
	}
	id, err := m.tasks.Submit(task.Meta{
		Module: "file", Action: "directory-size", Target: path,
		Username: session.User.Username, IP: ip, LockKey: "file:directory-size:" + path,
	}, func(parent context.Context, log *task.Log) (any, error) {
		ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
		defer cancel()
		size, err := treeSize(ctx, path)
		if err != nil {
			log.Line(err.Error())
			return nil, err
		}
		return gin.H{"size": size}, nil
	})
	if errors.Is(err, task.ErrBusy) {
		response.Fail(c, response.Conflict, "此目录正在计算大小")
		return
	}
	if err != nil {
		fail(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
