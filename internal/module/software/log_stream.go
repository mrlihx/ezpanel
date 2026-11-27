package software

import (
	"context"
	"os"
	"time"

	"ezpanel/internal/eventstream"
	"ezpanel/internal/logview"
	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

func (m *Module) streamSoftwareLog(c *gin.Context, path string) {
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	changes := make(chan struct{}, 1)
	last, _ := os.Lstat(path)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current, err := os.Lstat(path)
				changed := (current == nil) != (last == nil)
				if current != nil && last != nil {
					changed = !os.SameFile(last, current) || current.Size() != last.Size() || !current.ModTime().Equal(last.ModTime())
				}
				last = current
				if changed || (err != nil && !os.IsNotExist(err)) {
					select {
					case changes <- struct{}{}:
					default:
					}
				}
			}
		}
	}()
	eventstream.Serve(c, m.auth, m.tasks.Done(), changes, func(stream eventstream.Stream) bool {
		text, err := logview.Read(path)
		if err != nil {
			stream.Send("stream-error", "", response.Summary(err.Error()))
			return false
		}
		return stream.Send("logs", "", text)
	})
}
