package database

import (
	"context"
	"errors"
	"strings"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
)

// 同一服务器的批量操作持有一个任务锁，逐库执行并保留各项失败结果。
func (m *Module) batchDatabases(c *gin.Context) {
	s, ok := m.row(c)
	if !ok {
		return
	}
	var in struct {
		Action       string   `json:"action"`
		Databases    []string `json:"databases"`
		Host         string   `json:"host"`
		Password     string   `json:"password"`
		Confirmation string   `json:"confirmation"`
		Permanent    bool     `json:"permanent"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if len(in.Databases) < 1 || len(in.Databases) > 100 {
		response.Fail(c, response.Invalid, "请选择1至100个业务数据库")
		return
	}
	seen := map[string]bool{}
	names := []string{}
	for _, name := range in.Databases {
		if !identifier.MatchString(name) || protectedDB(name) {
			response.Fail(c, response.Invalid, "数据库名称无效或为系统数据库")
			return
		}
		if !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	switch in.Action {
	case "access":
		if _, err := normalizeAccessHost(in.Host); err != nil {
			response.Fail(c, response.Invalid, err.Error())
			return
		}
	case "password":
		if in.Password == "" || len(in.Password) > 1024 || strings.ContainsRune(in.Password, 0) {
			response.Fail(c, response.Invalid, "请输入1至1024字节的新密码")
			return
		}
	case "delete":
		if in.Confirmation != "删除数据库" {
			response.Fail(c, response.Invalid, "请输入删除数据库完成二次确认")
			return
		}
		if !in.Permanent {
			if _, err := clientExecutable(s, true); err != nil {
				response.Fail(c, response.Invalid, "缺少备份客户端，不支持回收；请安装客户端或明确选择永久删除")
				return
			}
		}
	default:
		response.Fail(c, response.Invalid, "批量操作类型无效")
		return
	}
	session := auth.Current(c)
	id, err := m.tasks.Submit(task.Meta{Module: "database", Action: in.Action, Target: s.Name + "/批量操作", ResourceID: s.ID, Username: session.User.Username, IP: m.auth.ClientIP(c), LockKey: LockKey(s.ID)}, func(ctx context.Context, log *task.Log) (any, error) {
		log.Secrets(in.Password)
		result := task.BatchResult{Results: []task.ItemResult{}}
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			var err error
			if in.Action == "delete" {
				_, err = m.deleteDatabase(ctx, s, name, in.Permanent, log)
			} else {
				itemCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				db, connectErr := m.connect(itemCtx, s, true)
				err = connectErr
				if err == nil {
					if in.Action == "access" {
						err = m.setDatabaseAccess(itemCtx, db, s.ID, name, in.Host)
					} else {
						err = m.setDatabasePassword(itemCtx, db, s.ID, name, in.Password)
					}
					db.Close()
				}
				cancel()
			}
			item := task.ItemResult{Target: name, Msg: "操作完成"}
			if err != nil {
				item.Code = response.System
				item.Msg = response.Summary(err.Error())
				result.FailedCount++
			} else {
				result.SuccessCount++
			}
			log.Line(name + "：" + item.Msg)
			result.Results = append(result.Results, item)
		}
		if result.FailedCount > 0 {
			return result, errors.New("部分数据库操作失败，请查看逐项结果")
		}
		return result, nil
	})
	if err != nil {
		failure(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
