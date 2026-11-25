package eventstream

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"ezpanel/internal/auth"
	"github.com/gin-gonic/gin"
)

type Stream struct {
	writer     http.ResponseWriter
	controller *http.ResponseController
}

func (s Stream) Send(event, id string, value any) bool {
	data, err := json.Marshal(value)
	if err != nil {
		return false
	}
	_ = s.controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if id != "" {
		if _, err := fmt.Fprintf(s.writer, "id: %s\n", id); err != nil {
			return false
		}
	}
	_, err = fmt.Fprintf(s.writer, "event: %s\ndata: %s\n\n", event, data)
	return err == nil && s.controller.Flush() == nil
}

func Serve(c *gin.Context, service *auth.Service, done <-chan struct{}, changes <-chan struct{}, send func(Stream) bool) {
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-store, no-transform")
	c.Header("X-Accel-Buffering", "no")
	s := Stream{writer: c.Writer, controller: http.NewResponseController(c.Writer)}
	_ = s.controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprint(s.writer, "retry: 2000\n\n"); err != nil || s.controller.Flush() != nil {
		return
	}
	if !send(s) {
		return
	}
	session := auth.Current(c)
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	recheck := time.NewTicker(auth.SessionRecheckInterval)
	defer recheck.Stop()
	delay := time.NewTimer(time.Hour)
	defer delay.Stop()
	delay.Stop()
	var ready <-chan time.Time
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-done:
			return
		case <-recheck.C:
			if _, err := service.CheckSession(session.Token); err != nil {
				s.Send("session-expired", "", nil)
				return
			}
		case <-heartbeat.C:
			_ = s.controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := fmt.Fprint(s.writer, ": ping\n\n"); err != nil || s.controller.Flush() != nil {
				return
			}
		case _, ok := <-changes:
			if !ok {
				changes = nil
				continue
			}
			// 合并密集写入，空闲时只发送心跳。
			if ready == nil {
				delay.Reset(250 * time.Millisecond)
				ready = delay.C
			}
		case <-ready:
			ready = nil
			if !send(s) {
				return
			}
		}
	}
}
