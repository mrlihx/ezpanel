package sysinfo

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

const dashboardDetailsInterval = 6 * time.Second

type dashboardProcessFilter struct {
	Sort    string `json:"sort"`
	Keyword string `json:"keyword"`
}

type dashboardNetworkFilter struct {
	Keyword        string `json:"keyword"`
	Page           int    `json:"page"`
	PageSize       int    `json:"pageSize"`
	PID            int32  `json:"pid"`
	Started        int64  `json:"started"`
	DetailPage     int    `json:"detailPage"`
	DetailPageSize int    `json:"detailPageSize"`
}

type dashboardSubscription struct {
	Processes *dashboardProcessFilter `json:"processes"`
	Network   *dashboardNetworkFilter `json:"network"`
}

func (sub dashboardSubscription) valid() bool {
	if p := sub.Processes; p != nil && ((p.Sort != "cpu" && p.Sort != "mem") || len(p.Keyword) > 1024) {
		return false
	}
	if n := sub.Network; n != nil {
		return len(n.Keyword) <= 1024 && n.Page > 0 && n.PageSize > 0 && n.PageSize <= 200 &&
			(n.PID == 0 || (n.PID > 0 && n.Started > 0 && n.DetailPage > 0 && n.DetailPageSize > 0 && n.DetailPageSize <= 200))
	}
	return true
}

type dashboardProcesses struct {
	Filter dashboardProcessFilter `json:"filter"`
	List   []Process              `json:"list"`
}

type dashboardNetwork struct {
	Filter      dashboardNetworkFilter `json:"filter"`
	List        gin.H                  `json:"list,omitempty"`
	Detail      gin.H                  `json:"detail,omitempty"`
	Error       string                 `json:"error,omitempty"`
	DetailError string                 `json:"detailError,omitempty"`
}

type dashboardFrame struct {
	Point     *Point               `json:"point,omitempty"`
	Overview  *Overview            `json:"overview,omitempty"`
	Top       map[string][]Process `json:"top,omitempty"`
	Processes *dashboardProcesses  `json:"processes,omitempty"`
	Network   *dashboardNetwork    `json:"network,omitempty"`
}

func (m *Module) populateDashboard(frame *dashboardFrame, filter *dashboardProcessFilter) {
	m.mu.RLock()
	overview, rows := m.overview, m.processes
	m.mu.RUnlock()
	frame.Overview = &overview
	frame.Top = map[string][]Process{"cpu": filterProcesses(rows["cpu"], "", 5), "mem": filterProcesses(rows["mem"], "", 5)}
	if filter != nil {
		frame.Processes = &dashboardProcesses{Filter: *filter, List: filterProcesses(rows[filter.Sort], filter.Keyword, 100)}
	}
}

func (m *Module) dashboardNetwork(ctx context.Context, filter dashboardNetworkFilter) dashboardNetwork {
	payload := dashboardNetwork{Filter: filter}
	snapshot, err := m.networkSnapshot(ctx)
	if err != nil {
		payload.Error = err.Error()
		return payload
	}
	payload.List = networkRows(snapshot, filter.Keyword, filter.Page, filter.PageSize)
	if filter.PID > 0 {
		payload.Detail, err = networkConnectionRows(ctx, snapshot, filter.PID, filter.Started, filter.DetailPage, filter.DetailPageSize)
		if err != nil {
			payload.DetailError = err.Error()
		}
	}
	return payload
}
