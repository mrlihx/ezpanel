package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mrlihx/ezpanel/sdk/go/panelsdk"
)

// DemoBackupStorage 实现 panelsdk.BackupStorage 接口，将备份归档安全存储于插件私有目录内。
// 面板通过 X-Panel-Storage-Request 传入渠道配置（Values）、ChannelID 与相对对象名（Key）。
type DemoBackupStorage struct {
	root  string
	state *StateStore
}

func NewDemoBackupStorage(dataDir string, state *StateStore) *DemoBackupStorage {
	return &DemoBackupStorage{
		root:  filepath.Join(dataDir, "backup-storage"),
		state: state,
	}
}

func cleanSegment(val string) (string, error) {
	val = strings.TrimSpace(val)
	if val == "" || strings.ContainsAny(val, "\\\x00") || strings.HasPrefix(val, "/") {
		return "", errors.New("路径段无效")
	}
	cleaned := filepath.ToSlash(filepath.Clean(val))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", errors.New("禁止路径穿越")
	}
	return cleaned, nil
}

func (d *DemoBackupStorage) resolveFolder(req panelsdk.StorageRequest) (string, string, error) {
	if strings.TrimSpace(req.Values["access-token"]) == "" {
		return "", "", errors.New("缺少访问令牌")
	}
	folder, err := cleanSegment(req.Values["folder"])
	if err != nil {
		return "", "", fmt.Errorf("存储子目录无效: %w", err)
	}
	dir := filepath.Join(d.root, filepath.FromSlash(folder))
	rel, err := filepath.Rel(d.root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", errors.New("存储目录越界")
	}
	return dir, folder, nil
}

func (d *DemoBackupStorage) resolveObject(req panelsdk.StorageRequest) (string, string, error) {
	dir, folder, err := d.resolveFolder(req)
	if err != nil {
		return "", folder, err
	}
	key, err := cleanSegment(req.Key)
	if err != nil {
		return "", folder, fmt.Errorf("对象键无效: %w", err)
	}
	target := filepath.Join(dir, filepath.FromSlash(key))
	rel, err := filepath.Rel(dir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", folder, errors.New("对象路径越界")
	}
	return target, folder, nil
}

// Test 校验渠道凭据并确保目标子目录可写；在「备份设置 → 测试连接」时由面板触发。
func (d *DemoBackupStorage) Test(_ context.Context, req panelsdk.StorageRequest) error {
	dir, folder, err := d.resolveFolder(req)
	if err == nil {
		if mkErr := os.MkdirAll(dir, 0700); mkErr != nil {
			err = mkErr
		} else if probe, pErr := os.CreateTemp(dir, ".probe-*"); pErr != nil {
			err = pErr
		} else {
			name := probe.Name()
			_ = probe.Close()
			_ = os.Remove(name)
		}
	}
	d.record("test", req, folder, 0, "", err)
	return err
}

// Upload 流式接收归档并写入同目录临时文件，校验配额后原子 Rename；若中途失败则由 defer 清理残留文件。
func (d *DemoBackupStorage) Upload(ctx context.Context, req panelsdk.StorageRequest, body io.Reader) error {
	target, folder, err := d.resolveObject(req)
	if err != nil {
		d.record("upload", req, folder, 0, "", err)
		return err
	}
	var limitBytes int64
	if raw := strings.TrimSpace(req.Values["quota-mb"]); raw != "" {
		mb, convErr := strconv.ParseInt(raw, 10, 64)
		if convErr != nil || mb <= 0 {
			err = errors.New("单文件上限配置无效")
			d.record("upload", req, folder, 0, "", err)
			return err
		}
		limitBytes = mb << 20
	}
	if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		d.record("upload", req, folder, 0, "", err)
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".upload-*")
	if err != nil {
		d.record("upload", req, folder, 0, "", err)
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		_ = tmp.Close()
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	hash := sha256.New()
	var written int64
	buf := make([]byte, 32<<10)
	for {
		if ctx.Err() != nil {
			err = ctx.Err()
			d.record("upload", req, folder, written, "", err)
			return err
		}
		n, readErr := body.Read(buf)
		if n > 0 {
			written += int64(n)
			if limitBytes > 0 && written > limitBytes {
				err = errors.New("归档大小超出渠道配额上限")
				d.record("upload", req, folder, written, "", err)
				return err
			}
			if _, wErr := tmp.Write(buf[:n]); wErr != nil {
				d.record("upload", req, folder, written, "", wErr)
				return wErr
			}
			_, _ = hash.Write(buf[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			d.record("upload", req, folder, written, "", readErr)
			return readErr
		}
	}
	if err = tmp.Sync(); err != nil {
		d.record("upload", req, folder, written, "", err)
		return err
	}
	if err = tmp.Close(); err != nil {
		d.record("upload", req, folder, written, "", err)
		return err
	}
	if err = os.Rename(tmpPath, target); err != nil {
		d.record("upload", req, folder, written, "", err)
		return err
	}
	cleanup = false
	digest := hex.EncodeToString(hash.Sum(nil))[:16]
	d.record("upload", req, folder, written, digest, nil)
	return nil
}

// Open 打开已存储的归档流；当对象不存在时必须返回 fs.ErrNotExist 以便面板识别。
func (d *DemoBackupStorage) Open(_ context.Context, req panelsdk.StorageRequest) (io.ReadCloser, error) {
	target, folder, err := d.resolveObject(req)
	if err != nil {
		d.record("open", req, folder, 0, "", err)
		return nil, err
	}
	f, err := os.Open(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			d.record("open", req, folder, 0, "", fs.ErrNotExist)
			return nil, fs.ErrNotExist
		}
		d.record("open", req, folder, 0, "", err)
		return nil, err
	}
	var size int64
	if info, statErr := f.Stat(); statErr == nil {
		size = info.Size()
	}
	d.record("open", req, folder, size, "", nil)
	return f, nil
}

// Delete 删除指定归档；对象已不存在时返回 fs.ErrNotExist（面板同样视为可接受状态）。
func (d *DemoBackupStorage) Delete(_ context.Context, req panelsdk.StorageRequest) error {
	target, folder, err := d.resolveObject(req)
	if err != nil {
		d.record("delete", req, folder, 0, "", err)
		return err
	}
	err = os.Remove(target)
	if errors.Is(err, os.ErrNotExist) {
		d.record("delete", req, folder, 0, "", fs.ErrNotExist)
		return fs.ErrNotExist
	}
	d.record("delete", req, folder, 0, "", err)
	return err
}

func (d *DemoBackupStorage) record(op string, req panelsdk.StorageRequest, folder string, size int64, digest string, err error) {
	if d.state == nil {
		return
	}
	rec := StorageOpRecord{
		Operation: op,
		ChannelID: req.ChannelID,
		Folder:    folder,
		Key:       req.Key,
		Size:      size,
		SHA256:    digest,
		Success:   err == nil,
		Time:      time.Now().UTC(),
	}
	if err != nil {
		rec.Error = err.Error()
	}
	d.state.AddStorageOp(rec)
}

type StoredObject struct {
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

func (d *DemoBackupStorage) ListObjects() ([]StoredObject, error) {
	if _, err := os.Stat(d.root); errors.Is(err, os.ErrNotExist) {
		return []StoredObject{}, nil
	}
	out := []StoredObject{}
	err := filepath.WalkDir(d.root, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(d.root, p)
		if err != nil {
			return nil
		}
		out = append(out, StoredObject{
			Path:       filepath.ToSlash(rel),
			Size:       info.Size(),
			ModifiedAt: info.ModTime(),
		})
		return nil
	})
	return out, err
}
