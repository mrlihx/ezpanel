package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/task"

	"github.com/gin-gonic/gin"
	"golang.org/x/sys/unix"
)

const uploadChunkSize = 8 << 20

type uploadSession struct {
	Target      string
	Fingerprint string
	Size        int64
	User        uint
	Overwrite   bool
}

func (m *Module) uploadRoot() string {
	return filepath.Join(m.cfg.Layout().TempDir(), "file-uploads")
}

func (m *Module) lockUpload(c *gin.Context, id string) (func(), bool) {
	unlock, err := m.tasks.Acquire("file:upload:" + id)
	if errors.Is(err, task.ErrBusy) {
		response.Fail(c, response.Conflict, "此文件正在上传，请稍后继续")
	} else if err != nil {
		fail(c, err)
	}
	return unlock, err == nil
}

func (m *Module) beginUpload(c *gin.Context) {
	var input struct {
		Dir         string `json:"dir"`
		Path        string `json:"path"`
		Size        int64  `json:"size"`
		Fingerprint string `json:"fingerprint"`
		Overwrite   bool   `json:"overwrite"`
	}
	if !response.Bind(c, &input) {
		return
	}
	parts := uploadPathParts(input.Path)
	if parts == nil || input.Size <= 0 || input.Size > 1<<53-1 || input.Fingerprint == "" || len(input.Fingerprint) > 512 {
		response.Fail(c, response.Invalid, "上传文件参数无效")
		return
	}
	dir, err := m.policy.Check(input.Dir, true)
	if err != nil {
		fail(c, err)
		return
	}
	info, err := os.Stat(dir)
	if err != nil {
		fail(c, err)
		return
	}
	if !info.IsDir() {
		response.Fail(c, response.Invalid, "上传目标必须为目录")
		return
	}
	target := filepath.Join(dir, input.Path)
	resolved, err := m.policy.CheckLink(target, true)
	if err != nil {
		fail(c, err)
		return
	}
	if resolved != target {
		fail(c, errConflict)
		return
	}
	if err = makeUploadDirectories(dir, parts[:len(parts)-1]); err != nil {
		fail(c, err)
		return
	}
	if _, err = uploadTargetInfo(target, input.Overwrite); err != nil {
		fail(c, err)
		return
	}

	session := uploadSession{target, input.Fingerprint, input.Size, auth.Current(c).User.ID, input.Overwrite}
	metadata, _ := json.Marshal(session)
	id := fmt.Sprintf("%x", sha256.Sum256(metadata))
	root := m.uploadRoot()
	if err = os.MkdirAll(root, 0700); err != nil {
		fail(c, err)
		return
	}
	m.cleanUploads(root)
	unlock, ok := m.lockUpload(c, id)
	if !ok {
		return
	}
	defer unlock()
	staging := filepath.Join(root, id)
	if err = os.MkdirAll(staging, 0700); err != nil {
		fail(c, err)
		return
	}
	if err = os.WriteFile(filepath.Join(staging, "upload.json"), metadata, 0600); err != nil {
		fail(c, err)
		return
	}
	f, err := os.OpenFile(filepath.Join(staging, "data.part"), os.O_CREATE|os.O_RDONLY|unix.O_NOFOLLOW, 0600)
	if err != nil {
		fail(c, err)
		return
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"id": id, "offset": info.Size(), "chunkSize": uploadChunkSize})
}

func (m *Module) cleanUploads(root string) {
	entries, _ := os.ReadDir(root)
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) != sha256.Size*2 {
			continue
		}
		staging := filepath.Join(root, entry.Name())
		info, err := os.Stat(filepath.Join(staging, "data.part"))
		if os.IsNotExist(err) {
			info, err = entry.Info()
		}
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		unlock, err := m.tasks.Acquire("file:upload:" + entry.Name())
		if err == nil {
			// 持锁后再取时间，避免删除刚恢复上传的分片。
			info, err := os.Stat(filepath.Join(staging, "data.part"))
			if os.IsNotExist(err) {
				info, err = os.Stat(staging)
			}
			if err == nil && info.ModTime().Before(cutoff) {
				_ = os.RemoveAll(staging)
			}
			unlock()
		}
	}
}

func (m *Module) withUpload(c *gin.Context, run func(string, uploadSession) error) {
	id := c.Param("id")
	if _, err := hex.DecodeString(id); err != nil || len(id) != sha256.Size*2 {
		response.Fail(c, response.Invalid, "上传编号无效")
		return
	}
	unlock, ok := m.lockUpload(c, id)
	if !ok {
		return
	}
	defer unlock()
	staging := filepath.Join(m.uploadRoot(), id)
	data, err := os.ReadFile(filepath.Join(staging, "upload.json"))
	var session uploadSession
	if err == nil {
		err = json.Unmarshal(data, &session)
	}
	if err == nil && session.User != auth.Current(c).User.ID {
		err = os.ErrNotExist
	}
	if err == nil {
		err = run(staging, session)
	}
	if err != nil {
		fail(c, err)
	}
}

func (m *Module) appendUpload(c *gin.Context) {
	offset, err := strconv.ParseInt(c.Query("offset"), 10, 64)
	length := c.Request.ContentLength
	if err != nil || offset < 0 || length <= 0 {
		response.Fail(c, response.Invalid, "分片参数无效")
		return
	}
	if length > uploadChunkSize {
		fail(c, errLimit)
		return
	}
	m.withUpload(c, func(staging string, session uploadSession) error {
		f, err := os.OpenFile(filepath.Join(staging, "data.part"), os.O_WRONLY|os.O_APPEND|unix.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if offset != info.Size() {
			response.Fail(c, response.Conflict, "分片偏移已变化，请重新选择文件继续上传")
			return nil
		}
		if length > session.Size-offset {
			return errLimit
		}
		// 只确认已落盘的分片，中断时保留上一个可用偏移。
		if _, err = io.CopyN(f, contextReader{c.Request.Context(), c.Request.Body}, length); err == nil {
			err = f.Sync()
		}
		if err != nil {
			return errors.Join(err, f.Truncate(offset))
		}
		response.OK(c, gin.H{"offset": offset + length})
		return nil
	})
}

func (m *Module) finishUpload(c *gin.Context) {
	m.withUpload(c, func(staging string, session uploadSession) error {
		partial := filepath.Join(staging, "data.part")
		info, err := os.Lstat(partial)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() != session.Size {
			response.Fail(c, response.Conflict, "文件分片尚未上传完成")
			return nil
		}
		target, err := m.policy.CheckLink(session.Target, true)
		if err != nil {
			return err
		}
		if target != session.Target {
			return errConflict
		}
		old, err := uploadTargetInfo(target, session.Overwrite)
		if err != nil {
			return err
		}
		if err = commitFile(partial, target, old, session.Overwrite, 0755); errors.Is(err, syscall.EXDEV) {
			err = m.copyUpload(c.Request.Context(), target, partial, session)
		}
		if err != nil {
			return err
		}
		_ = os.RemoveAll(staging)
		response.OK(c, nil)
		return nil
	})
}

func (m *Module) copyUpload(ctx context.Context, target, partial string, session uploadSession) error {
	f, err := os.Open(partial)
	if err != nil {
		return err
	}
	defer f.Close()
	return m.atomic(ctx, target, f, session.Overwrite, session.Size, 0755)
}

func uploadTargetInfo(path string, overwrite bool) (os.FileInfo, error) {
	old, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err == nil && (!overwrite || !old.Mode().IsRegular()) {
		return nil, errConflict
	}
	return old, err
}
