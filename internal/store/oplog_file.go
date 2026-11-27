package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

type OpLogFilter struct {
	Module          string
	Modules         []string
	Success         *bool
	Keyword         string
	Start           time.Time
	End             time.Time
	Kind            string // "login" | "operation" | ""
	ResourceID      string
	ResourceTaskIDs map[string]bool
	Page            int
	Size            int
	OrderAsc        bool
}

type OpLogIPStat struct {
	IP    string `json:"ip"`
	Count int64  `json:"count"`
}

type opLogFileStore struct {
	mu         sync.RWMutex
	path       string // empty => in-memory only (for :memory: tests)
	appendFile *os.File
	nextID     uint
	loaded     bool
	mem        []OpLog
	byTime     []int
	loadErr    error
}

var opLogStores sync.Map // map[gorm.ConnPool]*opLogFileStore

func opLogStoreFor(db *gorm.DB) *opLogFileStore {
	if db == nil || db.Config == nil || db.Config.ConnPool == nil {
		return nil
	}
	if v, ok := opLogStores.Load(db.Config.ConnPool); ok {
		return v.(*opLogFileStore)
	}
	s := &opLogFileStore{}
	actual, _ := opLogStores.LoadOrStore(db.Config.ConnPool, s)
	return actual.(*opLogFileStore)
}

// SetOpLogPath configures the JSONL file path used for panel operation logs on this DB instance.
func SetOpLogPath(db *gorm.DB, path string) {
	s := opLogStoreFor(db)
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.appendFile != nil {
		_ = s.appendFile.Close()
		s.appendFile = nil
	}
	s.path = path
	s.loaded = false
	s.mem = nil
	s.byTime = nil
	s.loadErr = nil
	s.nextID = 0
}

const maxOpLogEntries = 50000

func (s *opLogFileStore) ensureLoadedLocked() {
	if s.loaded {
		return
	}
	s.loadErr = nil
	if s.path == "" {
		s.loaded = true
		return
	}
	f, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.loaded = true
			return
		}
		s.loadErr = err
		slog.Error("读取操作日志失败", "error", err)
		return
	}
	defer f.Close()
	var loaded []OpLog
	var maxID uint
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item OpLog
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		if item.ID > maxID {
			maxID = item.ID
		}
		loaded = append(loaded, item)
	}
	if err := scanner.Err(); err != nil {
		s.loadErr = err
		slog.Error("操作日志未完整读入", "error", err)
		return
	}
	s.mem = loaded
	s.nextID = maxID
	s.indexLocked()
	s.loaded = true
}

func opLogEarlier(a, b OpLog) bool {
	if a.CreatedAt.Equal(b.CreatedAt) {
		return a.ID < b.ID
	}
	return a.CreatedAt.Before(b.CreatedAt)
}

func (s *opLogFileStore) indexLocked() {
	s.byTime = make([]int, len(s.mem))
	for i := range s.byTime {
		s.byTime[i] = i
	}
	sort.Slice(s.byTime, func(i, j int) bool {
		return opLogEarlier(s.mem[s.byTime[i]], s.mem[s.byTime[j]])
	})
}

func (s *opLogFileStore) append(entry *OpLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	if s.loadErr != nil {
		return s.loadErr
	}
	s.nextID++
	entry.ID = s.nextID
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	if s.path != "" {
		if err := os.MkdirAll(filepath.Dir(s.path), 0750); err != nil {
			return err
		}
		raw, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if s.appendFile == nil {
			f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			s.appendFile = f
		}
		if _, werr := s.appendFile.Write(append(raw, '\n')); werr != nil {
			_ = s.appendFile.Close()
			s.appendFile = nil
			return werr
		}
	}
	i := sort.Search(len(s.byTime), func(i int) bool {
		return opLogEarlier(*entry, s.mem[s.byTime[i]])
	})
	s.byTime = slices.Insert(s.byTime, i, len(s.mem))
	s.mem = append(s.mem, *entry)
	if len(s.mem) > maxOpLogEntries+5000 {
		keep := make([]OpLog, 0, maxOpLogEntries)
		for _, idx := range s.byTime[len(s.byTime)-maxOpLogEntries:] {
			keep = append(keep, s.mem[idx])
		}
		if err := s.rewriteLocked(keep); err == nil {
			s.mem = keep
			s.indexLocked()
		}
	}
	return nil
}

func matchOpLog(item OpLog, f OpLogFilter) bool {
	if f.Module != "" {
		if f.Module == "plugins" {
			if item.Module != "plugins" && !strings.HasPrefix(item.Module, "plugin:") {
				return false
			}
		} else if item.Module != f.Module {
			return false
		}
	}
	if len(f.Modules) > 0 {
		matched := false
		for _, m := range f.Modules {
			if item.Module == m {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if f.Success != nil && item.Success != *f.Success {
		return false
	}
	if f.Keyword != "" {
		user := ""
		if item.Username != nil {
			user = *item.Username
		}
		if !strings.Contains(item.Target, f.Keyword) &&
			!strings.Contains(item.Action, f.Keyword) &&
			!strings.Contains(item.IP, f.Keyword) &&
			!strings.Contains(user, f.Keyword) &&
			!strings.Contains(item.UserAgent, f.Keyword) {
			return false
		}
	}
	if !f.Start.IsZero() && item.CreatedAt.Before(f.Start) {
		return false
	}
	if !f.End.IsZero() && item.CreatedAt.After(f.End) {
		return false
	}
	switch f.Kind {
	case "login":
		if !(item.Module == "auth" && item.Action == "login") {
			return false
		}
	case "operation":
		if item.Module == "auth" && item.Action == "login" {
			return false
		}
	}
	if f.ResourceID != "" {
		inResource := item.ResourceID == f.ResourceID
		inTask := item.ResourceID == "" && item.TaskID != nil && f.ResourceTaskIDs[*item.TaskID]
		if !inResource && !inTask {
			return false
		}
	}
	return true
}

func (s *opLogFileStore) rewriteLocked(items []OpLog) error {
	if s.appendFile != nil {
		_ = s.appendFile.Close()
		s.appendFile = nil
	}
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0750); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".oplog-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	w := bufio.NewWriter(f)
	encoder := json.NewEncoder(w)
	for _, item := range items {
		if err := encoder.Encode(item); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.path)
}

func QueryOpLogs(db *gorm.DB, f OpLogFilter) ([]OpLog, int64) {
	s := opLogStoreFor(db)
	if s == nil {
		return []OpLog{}, 0
	}
	s.mu.RLock()
	if !s.loaded {
		s.mu.RUnlock()
		s.mu.Lock()
		s.ensureLoadedLocked()
		s.mu.Unlock()
		s.mu.RLock()
	}
	defer s.mu.RUnlock()

	var offset int
	if f.Size > 0 {
		page := max(f.Page, 1)
		if page-1 > len(s.mem)/f.Size {
			offset = len(s.mem)
		} else {
			offset = (page - 1) * f.Size
		}
	}
	result := []OpLog{}
	total := 0
	for i := range s.byTime {
		at := s.byTime[i]
		if !f.OrderAsc {
			at = s.byTime[len(s.byTime)-1-i]
		}
		item := s.mem[at]
		if !matchOpLog(item, f) {
			continue
		}
		if total >= offset && (f.Size <= 0 || len(result) < f.Size) {
			result = append(result, item)
		}
		total++
	}
	return result, int64(total)
}

func GetOpLog(db *gorm.DB, id uint64) (OpLog, error) {
	s := opLogStoreFor(db)
	if s == nil {
		return OpLog{}, gorm.ErrRecordNotFound
	}
	s.mu.RLock()
	if !s.loaded {
		s.mu.RUnlock()
		s.mu.Lock()
		s.ensureLoadedLocked()
		s.mu.Unlock()
		s.mu.RLock()
	}
	defer s.mu.RUnlock()
	for i := len(s.mem) - 1; i >= 0; i-- {
		if uint64(s.mem[i].ID) == id {
			return s.mem[i], nil
		}
	}
	return OpLog{}, gorm.ErrRecordNotFound
}

func ClearOpLogs(db *gorm.DB, f OpLogFilter, before *time.Time) (int64, error) {
	s := opLogStoreFor(db)
	if s == nil {
		return 0, errors.New("日志存储未初始化")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	if s.loadErr != nil {
		return 0, s.loadErr
	}
	kept := make([]OpLog, 0, len(s.mem))
	var deleted int64
	for _, item := range s.mem {
		del := matchOpLog(item, f)
		if del && before != nil && !item.CreatedAt.Before(*before) {
			del = false
		}
		if del {
			deleted++
		} else {
			kept = append(kept, item)
		}
	}
	if deleted > 0 {
		if err := s.rewriteLocked(kept); err != nil {
			return 0, err
		}
		s.mem = kept
		s.indexLocked()
	}
	return deleted, nil
}

func OpLogIPStats(db *gorm.DB, f OpLogFilter) []OpLogIPStat {
	s := opLogStoreFor(db)
	if s == nil {
		return []OpLogIPStat{}
	}
	s.mu.RLock()
	if !s.loaded {
		s.mu.RUnlock()
		s.mu.Lock()
		s.ensureLoadedLocked()
		s.mu.Unlock()
		s.mu.RLock()
	}
	counts := map[string]int64{}
	filter := OpLogFilter{
		Module:          f.Module,
		Modules:         f.Modules,
		Success:         f.Success,
		Keyword:         f.Keyword,
		Start:           f.Start,
		End:             f.End,
		Kind:            f.Kind,
		ResourceID:      f.ResourceID,
		ResourceTaskIDs: f.ResourceTaskIDs,
	}
	for _, item := range s.mem {
		if matchOpLog(item, filter) {
			counts[item.IP]++
		}
	}
	s.mu.RUnlock()
	out := make([]OpLogIPStat, 0, len(counts))
	for ip, count := range counts {
		out = append(out, OpLogIPStat{IP: ip, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].IP < out[j].IP
	})
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

func registerOpLogCallbacks(db *gorm.DB, dbPath string) {
	if dbPath != ":memory:" && dbPath != "" {
		SetOpLogPath(db, filepath.Join(filepath.Dir(dbPath), "op_logs.jsonl"))
	} else {
		SetOpLogPath(db, "")
	}
	origCreate := db.Callback().Create().Get("gorm:create")
	if origCreate == nil {
		return
	}
	_ = db.Callback().Create().Replace("gorm:create", func(tx *gorm.DB) {
		if tx.Error != nil {
			return
		}
		if tx.Statement != nil && tx.Statement.Schema != nil && tx.Statement.Schema.Table == "op_logs" {
			s := opLogStoreFor(tx)
			if s == nil {
				return
			}
			switch v := tx.Statement.Dest.(type) {
			case *OpLog:
				if err := s.append(v); err != nil {
					_ = tx.AddError(err)
				}
				tx.RowsAffected = 1
				return
			case *[]OpLog:
				for i := range *v {
					if err := s.append(&(*v)[i]); err != nil {
						_ = tx.AddError(err)
						return
					}
				}
				tx.RowsAffected = int64(len(*v))
				return
			}
		}
		origCreate(tx)
	})
}
