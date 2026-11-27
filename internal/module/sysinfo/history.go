package sysinfo

import (
	"strconv"
	"time"

	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

const historySpan = time.Hour

// 调用时已持有 m.mu。保留原始采样，缩小选区时仍能取得细节。
func (m *Module) recordPoint(point Point) {
	cutoff := point.TS.Add(-historySpan)
	for len(m.history) > 0 && m.history[0].TS.Before(cutoff) {
		m.history[0] = Point{}
		m.history = m.history[1:]
	}
	m.history = append(m.history, point)
}

func (m *Module) HistoryPoints(c *gin.Context) {
	start, startErr := strconv.ParseInt(c.Query("start"), 10, 64)
	end, endErr := strconv.ParseInt(c.Query("end"), 10, 64)
	if startErr != nil || endErr != nil || start > end {
		response.Fail(c, response.Invalid, "时间范围无效")
		return
	}
	points := []Point{}
	m.mu.RLock()
	start = max(start, m.point.TS.Add(-historySpan).UnixMilli())
	end = min(end, m.point.TS.UnixMilli())
	// 大区间抽样返回，避免用户拖动选区时传输整段原始数据。
	if start <= end {
		step := max(int64(1), (end-start)/360)
		next := start
		for _, point := range m.history {
			ts := point.TS.UnixMilli()
			if ts > end {
				break
			}
			if ts >= next {
				points = append(points, point)
				next = ts + step
			}
		}
	}
	m.mu.RUnlock()
	response.OK(c, points)
}
