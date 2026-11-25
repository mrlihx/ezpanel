package file

import (
	"context"
	"errors"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func (m *Module) trashState() (bool, int64, int64, error) {
	var setting store.Setting
	e := m.db.Where("key = ?", "trash.enabled").First(&setting).Error
	if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
		return false, 0, 0, e
	}
	enabled := setting.Value != "false"
	var stats struct {
		Count int64
		Total int64
	}
	e = m.db.Model(&store.TrashItem{}).Select("count(*) as count, COALESCE(sum(size),0) as total").Scan(&stats).Error
	return enabled, stats.Total, stats.Count, e
}
func (m *Module) trashConfig(c *gin.Context) {
	enabled, total, count, e := m.trashState()
	if e != nil {
		fail(c, e)
		return
	}
	response.OK(c, gin.H{"enabled": enabled, "totalSize": total, "count": count, "maxSize": m.cfg.TrashMaxBytes})
}
func (m *Module) setTrashConfig(c *gin.Context) {
	var r struct {
		Enabled *bool `json:"enabled"`
	}
	if !response.Bind(c, &r) {
		return
	}
	if r.Enabled == nil {
		response.Fail(c, response.Invalid, "缺少开关参数")
		return
	}
	value := "false"
	if *r.Enabled {
		value = "true"
	}
	e := m.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&store.Setting{Key: "trash.enabled", Value: value}).Error
	if e != nil {
		fail(c, e)
		return
	}
	response.OK(c, nil)
}
func fileType(name string, isDir bool) string {
	if isDir {
		return "dir"
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	for _, v := range strings.Fields("jpg jpeg png gif webp bmp svg") {
		if ext == v {
			return "image"
		}
	}
	for _, v := range strings.Fields("txt md pdf doc docx xls xlsx ppt pptx csv") {
		if ext == v {
			return "doc"
		}
	}
	return "file"
}
func (m *Module) trashList(c *gin.Context) {
	page, size, ok := response.Page(c, 100, 20)
	if !ok {
		return
	}
	q := m.db.Model(&store.TrashItem{})
	switch c.DefaultQuery("type", "all") {
	case "all":
	case "file":
		q = q.Where("is_dir = ?", false)
	case "dir", "image", "doc":
		q = q.Where("file_type = ?", c.Query("type"))
	default:
		response.Fail(c, response.Invalid, "类型无效")
		return
	}
	if keyword := c.Query("keyword"); keyword != "" {
		q = q.Where("instr(lower(name),lower(?)) > 0", keyword)
	}
	var count int64
	var rows []store.TrashItem
	if e := q.Count(&count).Error; e != nil {
		fail(c, e)
		return
	}
	if e := q.Order("deleted_at DESC, id ASC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; e != nil {
		fail(c, e)
		return
	}
	response.OK(c, gin.H{"list": rows, "total": count})
}
func (m *Module) delete(c *gin.Context) {
	var r struct {
		Paths     []string `json:"paths"`
		Permanent bool     `json:"permanent"`
	}
	if !response.Bind(c, &r) {
		return
	}
	paths, e := m.sources(r.Paths, true)
	if e != nil {
		fail(c, e)
		return
	}
	for _, p := range paths {
		info, e := os.Lstat(p)
		if e != nil {
			fail(c, e)
			return
		}
		if info.IsDir() {
			if e = m.policy.CheckTree(p); e != nil {
				fail(c, e)
				return
			}
		}
	}
	enabled, _, _, e := m.trashState()
	if e != nil {
		fail(c, e)
		return
	}
	m.submit(c, "delete", paths, func(ctx context.Context, p string) error {
		if _, e := m.policy.CheckLink(p, true); e != nil {
			return e
		}
		if r.Permanent || !enabled {
			return removeTree(ctx, p)
		}
		return m.trashPut(ctx, p)
	})
}
func (m *Module) trashPut(ctx context.Context, p string) error {
	size, e := treeSize(ctx, p)
	if e != nil {
		return e
	}
	_, total, _, e := m.trashState()
	if e != nil {
		return e
	}
	if size > m.cfg.TrashMaxBytes-total {
		return errLimit
	}
	info, e := os.Lstat(p)
	if e != nil {
		return e
	}
	id := store.ID()
	dst := filepath.Join(m.cfg.Layout().TrashDir(), "files", id)
	e = m.rename(p, dst)
	if errors.Is(e, syscall.EXDEV) {
		tmp := dst + ".part"
		if e = copyTree(ctx, p, tmp); e != nil {
			os.RemoveAll(tmp)
			return e
		}
		if e = os.Rename(tmp, dst); e != nil {
			os.RemoveAll(tmp)
			return e
		}
		if e = m.remove(ctx, p); e != nil {
			row := store.TrashItem{ID: id, Name: filepath.Base(p), OriginalPath: p, StoragePath: dst, IsDir: info.IsDir(), FileType: fileType(p, info.IsDir()), Size: size, DeletedAt: time.Now().UTC()}
			m.db.Create(&row)
			return errors.New("源删除失败，源与回收站副本可能同时存在，需人工检查")
		}
	}
	if e != nil {
		return e
	}
	row := store.TrashItem{ID: id, Name: filepath.Base(p), OriginalPath: p, StoragePath: dst, IsDir: info.IsDir(), FileType: fileType(p, info.IsDir()), Size: size, DeletedAt: time.Now().UTC()}
	if e = m.db.Create(&row).Error; e != nil {
		return errors.New("元数据写入失败，数据保留在回收站暂存区，需人工处理")
	}
	return nil
}
func (m *Module) trashRows(c *gin.Context) ([]store.TrashItem, bool) {
	var r struct {
		IDs       []string `json:"ids"`
		Overwrite bool     `json:"overwrite"`
	}
	if !response.Bind(c, &r) {
		return nil, false
	}
	if len(r.IDs) == 0 || len(r.IDs) > 100 {
		response.Fail(c, response.Invalid, "每批需要1至100项")
		return nil, false
	}
	seen := map[string]bool{}
	for _, id := range r.IDs {
		if id == "" || seen[id] {
			response.Fail(c, response.Invalid, "条目ID无效或重复")
			return nil, false
		}
		seen[id] = true
	}
	var found []store.TrashItem
	if e := m.db.Where("id IN ?", r.IDs).Find(&found).Error; e != nil {
		response.Fail(c, response.Internal, "")
		return nil, false
	}
	byID := make(map[string]store.TrashItem, len(found))
	for _, item := range found {
		byID[item.ID] = item
	}
	rows := make([]store.TrashItem, 0, len(r.IDs))
	for _, id := range r.IDs {
		row, ok := byID[id]
		if !ok {
			response.Fail(c, response.NotFound, "回收站条目不存在")
			return nil, false
		}
		rows = append(rows, row)
	}
	c.Set("file.overwrite", r.Overwrite)
	return rows, true
}
func (m *Module) restore(c *gin.Context) {
	rows, ok := m.trashRows(c)
	if !ok {
		return
	}
	for _, row := range rows {
		if _, e := m.policy.CheckLink(row.OriginalPath, true); e != nil {
			fail(c, e)
			return
		}
	}
	overwrite := c.GetBool("file.overwrite")
	executable := false
	for _, row := range rows {
		src, e := os.Lstat(row.StoragePath)
		if e != nil {
			continue
		}
		old, e := os.Lstat(row.OriginalPath)
		if os.IsNotExist(e) || e == nil && overwrite && src.Mode().IsRegular() && old.Mode().IsRegular() {
			executable = true
		}
	}
	if !executable {
		fail(c, errConflict)
		return
	}
	ids := make([]string, len(rows))
	byID := map[string]store.TrashItem{}
	for i, row := range rows {
		ids[i] = row.ID
		byID[row.ID] = row
	}
	m.submit(c, "restore", ids, func(ctx context.Context, id string) error {
		row := byID[id]
		dst, e := m.policy.CheckLink(row.OriginalPath, true)
		if e != nil {
			return e
		}
		if e = m.policy.CheckTree(dst); e != nil {
			return e
		}
		srcInfo, e := os.Lstat(row.StoragePath)
		if e != nil {
			return e
		}
		old, e := os.Lstat(dst)
		if e == nil && (!overwrite || !srcInfo.Mode().IsRegular() || !old.Mode().IsRegular()) {
			return errConflict
		}
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e = os.MkdirAll(filepath.Dir(dst), 0755); e != nil {
			return e
		}
		if srcInfo.Mode().IsRegular() {
			f, e := os.Open(row.StoragePath)
			if e != nil {
				return e
			}
			e = m.atomic(ctx, dst, f, overwrite, 1<<63-2, 0644)
			f.Close()
			if e != nil {
				return e
			}
			if e = metadata(dst, srcInfo); e != nil {
				return e
			}
			if e = os.Remove(row.StoragePath); e != nil {
				return e
			}
		} else {
			tmp := filepath.Join(filepath.Dir(dst), ".panel-"+store.ID()+".part")
			defer os.RemoveAll(tmp)
			if e = copyTree(ctx, row.StoragePath, tmp); e != nil {
				return e
			}
			if e = os.Rename(tmp, dst); e != nil {
				return e
			}
			if e = removeTree(ctx, row.StoragePath); e != nil {
				return e
			}
		}
		return m.db.Delete(&store.TrashItem{}, "id = ?", id).Error
	})
}
func (m *Module) purgeRows(c *gin.Context, rows []store.TrashItem) {
	ids := make([]string, len(rows))
	byID := map[string]store.TrashItem{}
	for i, row := range rows {
		ids[i] = row.ID
		byID[row.ID] = row
	}
	m.submit(c, "trash-delete", ids, func(ctx context.Context, id string) error {
		row := byID[id]
		if e := removeTree(ctx, row.StoragePath); e != nil && !os.IsNotExist(e) {
			return e
		}
		return m.db.Delete(&store.TrashItem{}, "id = ?", id).Error
	})
}
func (m *Module) purge(c *gin.Context) {
	rows, ok := m.trashRows(c)
	if ok {
		m.purgeRows(c, rows)
	}
}
func (m *Module) empty(c *gin.Context) {
	var in struct {
		Confirmation string `json:"confirmation"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Confirmation != "确认清空文件回收站" {
		response.Fail(c, response.Invalid, "请输入确认清空文件回收站")
		return
	}
	var rows []store.TrashItem
	if e := m.db.Order("deleted_at ASC").Find(&rows).Error; e != nil {
		fail(c, e)
		return
	}
	m.purgeRows(c, rows)
}
