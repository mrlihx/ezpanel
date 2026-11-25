package database

import (
	"context"
	"encoding/json"
	"strings"

	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func (m *Module) dropMongoCollection(c *gin.Context) {
	db, collection, ok := mongoTarget(c)
	if !ok {
		return
	}
	if collection == "" || db == "admin" || db == "local" || db == "config" || strings.HasPrefix(collection, "system.") {
		response.Fail(c, response.Invalid, "请选择可删除的业务集合")
		return
	}
	var in struct {
		ConfirmName string `json:"confirmName"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.ConfirmName != collection {
		response.Fail(c, response.Invalid, "请输入集合名称确认删除")
		return
	}
	m.withMongo(c, func(ctx context.Context, client *mongo.Client) error {
		if err := client.Database(db).Collection(collection).Drop(ctx); err != nil {
			return err
		}
		response.OK(c, nil)
		return nil
	})
}
func (m *Module) changeMongoDocument(c *gin.Context) {
	db, collection, ok := mongoTarget(c)
	if !ok {
		return
	}
	if collection == "" || db == "admin" || db == "local" || db == "config" || strings.HasPrefix(collection, "system.") {
		response.Fail(c, response.Invalid, "请选择可编辑的业务集合")
		return
	}
	var in struct {
		ID       json.RawMessage `json:"id"`
		Document json.RawMessage `json:"document"`
	}
	if !response.Bind(c, &in) {
		return
	}
	var wrapped bson.D
	if len(in.ID) == 0 || bson.UnmarshalExtJSON(append(append([]byte(`{"_id":`), in.ID...), '}'), false, &wrapped) != nil {
		response.Fail(c, response.Invalid, "文档ID须为有效Extended JSON值")
		return
	}
	filter := bson.D{{Key: "_id", Value: bson.D{{Key: "$eq", Value: wrapped[0].Value}}}}
	var document bson.D
	if c.Request.Method == "PUT" {
		if bson.UnmarshalExtJSON(in.Document, false, &document) != nil {
			response.Fail(c, response.Invalid, "文档须为合法Extended JSON对象")
			return
		}
	}
	m.withMongo(c, func(ctx context.Context, client *mongo.Client) error {
		coll := client.Database(db).Collection(collection)
		var count int64
		if c.Request.Method == "DELETE" {
			result, err := coll.DeleteOne(ctx, filter)
			if err != nil {
				return err
			}
			count = result.DeletedCount
		} else {
			result, err := coll.ReplaceOne(ctx, filter, document)
			if err != nil {
				return err
			}
			count = result.MatchedCount
		}
		if count == 0 {
			response.Fail(c, response.NotFound, "文档已不存在，请刷新列表")
			return nil
		}
		response.OK(c, nil)
		return nil
	})
}
