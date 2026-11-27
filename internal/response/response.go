package response

import (
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	Invalid      = 1001
	Unauthorized = 1002
	Forbidden    = 1003
	NotFound     = 1004
	Conflict     = 1005
	TooLarge     = 1006
	RateLimited  = 1007
	System       = 2001
	Internal     = 5000
)

var failHTTPStatus = map[int]int{
	Invalid:     400,
	Unauthorized: 401,
	Forbidden:   403,
	NotFound:    404,
	Conflict:    409,
	TooLarge:    413,
	RateLimited: 429,
	System:      500,
	Internal:    500,
	3001:        400,
	3002:        409,
	3003:        500,
	3004:        503,
}

func OK(c *gin.Context, data any) { c.JSON(200, gin.H{"code": 0, "msg": "ok", "data": data}) }
func Fail(c *gin.Context, code int, msg string) {
	status := failHTTPStatus[code]
	if status == 0 {
		status = 500
		code = Internal
	}
	if code == Internal {
		msg = "内部错误"
	}
	c.Set("panel.errorCode", code)
	c.AbortWithStatusJSON(status, gin.H{"code": code, "msg": Summary(msg), "data": nil})
}
func Bind(c *gin.Context, dst any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20)
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	e := dec.Decode(dst)
	if e == nil {
		var rest any
		if trailing := dec.Decode(&rest); trailing != io.EOF {
			if trailing != nil {
				e = trailing
			} else {
				e = errors.New("只允许一个JSON对象")
			}
		}
	}
	if e != nil {
		var max *http.MaxBytesError
		if errors.As(e, &max) {
			Fail(c, TooLarge, "请求体超过4MiB")
		} else {
			Fail(c, Invalid, "JSON参数无效")
		}
		return false
	}
	return true
}
func Page(c *gin.Context, max, defaultSize int) (page, size int, ok bool) {
	page, size = 1, defaultSize
	for _, v := range []struct {
		key string
		dst *int
	}{{"page", &page}, {"page_size", &size}} {
		if s, exists := c.GetQuery(v.key); exists {
			n, e := strconv.Atoi(s)
			if e != nil || n < 1 {
				Fail(c, Invalid, "分页参数无效")
				return 0, 0, false
			}
			*v.dst = n
		}
	}
	if size > max {
		Fail(c, Invalid, "分页大小超过上限")
		return 0, 0, false
	}
	if page > math.MaxInt/size {
		Fail(c, Invalid, "页码超过可用范围")
		return 0, 0, false
	}
	return page, size, true
}

// Summary 只处理受控摘要；调用方不得传入请求正文、脚本或SQL。
func Summary(s string, secrets ...string) string {
	s = strings.ToValidUTF8(s, "�")
	for _, v := range secrets {
		if v != "" {
			s = strings.ReplaceAll(s, v, "[REDACTED]")
		}
	}
	if len(s) > 2048 {
		s = s[:2048]
		for !utf8.ValidString(s) && len(s) > 0 {
			s = s[:len(s)-1]
		}
	}
	return s
}
