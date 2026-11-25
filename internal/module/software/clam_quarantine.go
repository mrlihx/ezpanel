package software

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"syscall"
	"time"

	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

var clamID = regexp.MustCompile(`^[0-9a-f]{32}$`)

type quarantinedFile struct {
	ID     string    `json:"id"`
	Path   string    `json:"path"`
	Source string    `json:"source"`
	Time   time.Time `json:"time"`
}

func (m *Module) quarantineFile(source, root string) error {
	real, err := filepath.EvalSymlinks(source)
	if err != nil || real != source || !pathsafe.Contains(root, real) {
		return errors.New("感染文件路径已变化或指向目录之外")
	}
	row := quarantinedFile{ID: store.ID(), Path: source, Source: root, Time: time.Now().UTC()}
	dir := filepath.Join(m.cfg.DataDir(), "clamav-quarantine", row.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, _ := json.Marshal(row)
	err = os.WriteFile(filepath.Join(dir, "record.json"), data, 0600)
	if err == nil {
		err = moveQuarantined(source, filepath.Join(dir, "payload"))
	}
	if err != nil {
		_ = os.RemoveAll(dir)
	}
	return err
}

// 硬链接保留原权限和属主，跨磁盘时先完成复制再移除源文件。
func moveQuarantined(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("隔离操作仅支持普通文件")
	}
	if err = os.Link(src, dst); errors.Is(err, syscall.EXDEV) {
		input, openErr := os.Open(src)
		if openErr != nil {
			return openErr
		}
		defer input.Close()
		output, openErr := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if openErr != nil {
			return openErr
		}
		_, err = io.Copy(output, input)
		if err == nil {
			owner := info.Sys().(*syscall.Stat_t)
			err = output.Chown(int(owner.Uid), int(owner.Gid))
		}
		if err == nil {
			err = output.Chmod(info.Mode().Perm())
		}
		if err = errors.Join(err, output.Close()); err != nil {
			_ = os.Remove(dst)
			return err
		}
	} else if err != nil {
		return err
	}
	if err = os.Remove(src); err != nil {
		_ = os.Remove(dst)
	}
	return err
}

func (m *Module) clamQuarantine(c *gin.Context) {
	root := filepath.Join(m.cfg.DataDir(), "clamav-quarantine")
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		fail(c, err)
		return
	}
	page, size, ok := response.Page(c, 100, 20)
	if !ok {
		return
	}
	list := []quarantinedFile{}
	for _, entry := range entries {
		if !entry.IsDir() || !clamID.MatchString(entry.Name()) {
			continue
		}
		var row quarantinedFile
		data, err := os.ReadFile(filepath.Join(root, entry.Name(), "record.json"))
		if err == nil && json.Unmarshal(data, &row) == nil {
			if _, err := os.Stat(filepath.Join(root, entry.Name(), "payload")); err == nil {
				list = append(list, row)
			}
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Time.After(list[j].Time) })
	start := min(len(list), (page-1)*size)
	response.OK(c, gin.H{"list": list[start:min(len(list), start+size)], "total": len(list)})
}

func (m *Module) clamQuarantineAction(c *gin.Context) {
	var in struct {
		IDs    []string `json:"ids"`
		Action string   `json:"action"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if len(in.IDs) == 0 || len(in.IDs) > 100 || (in.Action != "delete" && in.Action != "restore") {
		response.Fail(c, response.Invalid, "隔离文件操作无效")
		return
	}
	for _, id := range in.IDs {
		if !clamID.MatchString(id) {
			response.Fail(c, response.Invalid, "隔离文件标识无效")
			return
		}
	}
	release, err := m.tasks.Acquire(clamAVLock)
	if err != nil {
		fail(c, err)
		return
	}
	defer release()
	failed := map[string]string{}
	for _, id := range in.IDs {
		dir := filepath.Join(m.cfg.DataDir(), "clamav-quarantine", id)
		if in.Action == "restore" {
			var row quarantinedFile
			data, err := os.ReadFile(filepath.Join(dir, "record.json"))
			if err != nil || json.Unmarshal(data, &row) != nil {
				failed[id] = "无法读取原始文件路径"
				continue
			}
			root, err := m.scanDirectory(row.Source)
			if err != nil || !pathsafe.Contains(root, row.Path) {
				failed[id] = "原始目录不可用"
				continue
			}
			parent, err := filepath.EvalSymlinks(filepath.Dir(row.Path))
			if err != nil || parent != filepath.Dir(row.Path) {
				failed[id] = "原始目录已变化"
				continue
			}
			if err = moveQuarantined(filepath.Join(dir, "payload"), row.Path); err != nil {
				failed[id] = response.Summary(err.Error())
				continue
			}
		}
		if err := os.RemoveAll(dir); err != nil {
			failed[id] = response.Summary(err.Error())
		}
	}
	response.OK(c, gin.H{"failed": failed})
}
