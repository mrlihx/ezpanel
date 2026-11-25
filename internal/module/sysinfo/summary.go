package sysinfo

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type SummaryCount struct {
	Value *int64 `json:"value"`
	Error string `json:"error,omitempty"`
}

// SetSummarySources 在路由开始服务前注入，复用数据库模块的连接配置。
func (m *Module) SetSummarySources(db *gorm.DB, countDatabases func(context.Context) (int64, error)) {
	m.db, m.countDatabases = db, countDatabases
}

func (m *Module) summary(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	result := make([]SummaryCount, 4)
	readers := []func() (int64, error){
		func() (int64, error) {
			var n int64
			if m.db == nil {
				return 0, errors.New("网站统计未配置")
			}
			if e := m.db.WithContext(ctx).Model(&store.Website{}).Count(&n).Error; e != nil {
				return 0, errors.New("网站数量读取失败")
			}
			return n, nil
		},
		func() (int64, error) {
			if m.countDatabases == nil {
				return 0, errors.New("数据库统计未配置")
			}
			return m.countDatabases(ctx)
		},
		func() (int64, error) { return dockerCount(ctx) },
		func() (int64, error) {
			var n int64
			if m.db == nil {
				return 0, errors.New("计划任务统计未配置")
			}
			if e := m.db.WithContext(ctx).Model(&store.CronJob{}).Count(&n).Error; e != nil {
				return 0, errors.New("计划任务数量读取失败")
			}
			return n, nil
		},
	}
	var pending sync.WaitGroup
	for i, read := range readers {
		pending.Go(func() {
			n, e := read()
			if e != nil {
				result[i].Error = response.Summary(e.Error())
				return
			}
			result[i].Value = &n
		})
	}
	pending.Wait()
	response.OK(c, gin.H{"websites": result[0], "databases": result[1], "docker": result[2], "crons": result[3]})
}

func dockerCount(ctx context.Context) (int64, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/json?all=1", nil)
	if e != nil {
		return 0, errors.New("Docker统计请求创建失败")
	}
	res, e := client.Do(req)
	if e != nil {
		return 0, errors.New("本机Docker不可访问或读取超时")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 0, errors.New("本机Docker容器列表读取失败")
	}
	var containers []json.RawMessage
	if json.NewDecoder(res.Body).Decode(&containers) != nil || containers == nil {
		return 0, errors.New("本机Docker返回的容器列表无效")
	}
	return int64(len(containers)), nil
}
