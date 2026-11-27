package database

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"fmt"
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

var mongoDatabaseName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,63}$`)
var mongoCollectionName = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,119}$`)

type noSQLConnection struct {
	InstanceID  string `json:"instanceId,omitempty"`
	ID          string `json:"id"`
	IsLocal     bool   `json:"local"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	PasswordEnc string `json:"passwordEnc,omitempty"`
	TLS         bool   `json:"tls"`
	AuthSource  string `json:"authSource"`
}

func (m *Module) noSQLRoutes(r *gin.RouterGroup) {
	n := r.Group("/nosql/:engine")
	n.Use(func(c *gin.Context) {
		if c.Param("engine") != "mongodb" && c.Param("engine") != "redis" {
			response.Fail(c, response.Invalid, "数据库类型无效")
			c.Abort()
			return
		}
	})
	n.GET("/env", m.noSQLEnvironment)
	n.GET("/connections", m.noSQLConnections)
	n.POST("/connections", m.saveNoSQLConnection)
	n.DELETE("/connections/:connection", m.deleteNoSQLConnection)
	n.POST("/connections/:connection/password/reveal", m.revealNoSQLConnectionPassword)
	n.POST("/connections/:connection/root-password/reveal", m.mongoRootPassword)
	n.PUT("/connections/:connection/root-password", m.mongoRootPassword)
	n.GET("/connections/:connection/databases", m.noSQLDatabases)
	n.POST("/connections/:connection/databases", m.createNoSQLDatabase)
	n.DELETE("/connections/:connection/databases/:database", m.dropNoSQLDatabase)
	n.PUT("/connections/:connection/databases/:database/metadata", m.mongoMetadata)
	n.POST("/connections/:connection/databases/:database/account", m.createMongoDatabaseAccount)
	n.POST("/connections/:connection/databases/:database/password/reveal", m.revealMongoPassword)
	n.POST("/connections/:connection/batch", m.mongoBatch)
	n.GET("/connections/:connection/collections", m.mongoCollections)
	n.POST("/connections/:connection/collections", m.createMongoCollection)
	n.DELETE("/connections/:connection/collections", m.dropMongoCollection)
	n.GET("/connections/:connection/documents", m.mongoDocuments)
	n.POST("/connections/:connection/documents", m.insertMongoDocument)
	n.PUT("/connections/:connection/documents", m.changeMongoDocument)
	n.DELETE("/connections/:connection/documents", m.changeMongoDocument)
	n.GET("/connections/:connection/keys", m.redisKeys)
	n.GET("/connections/:connection/key-list", m.redisKeyList)
	n.DELETE("/connections/:connection/key-list", m.deleteRedisKeys)
	n.GET("/connections/:connection/value", m.redisValue)
	n.PUT("/connections/:connection/value", m.setRedisValue)
	n.DELETE("/connections/:connection/value", m.deleteRedisValue)
}
func (m *Module) noSQLConnections(c *gin.Context) {
	ctx, engine := c.Request.Context(), c.Param("engine")
	installed, err := m.ensureNoSQLLocal(ctx, engine)
	if err != nil {
		failure(c, err)
		return
	}
	instances := m.localInstances(ctx, engine)
	if err := m.syncNoSQLInstances(ctx, engine, instances); err != nil {
		failure(c, err)
		return
	}
	var rows []store.Setting
	if err := m.db.Where("key LIKE ?", "database."+engine+".%").Order("key").Find(&rows).Error; err != nil {
		failure(c, err)
		return
	}
	out := []gin.H{}
	for _, row := range rows {
		var v noSQLConnection
		if err := json.Unmarshal([]byte(row.Value), &v); err != nil {
			failure(c, err)
			return
		}
		item := gin.H{"local": v.IsLocal, "id": v.ID, "name": v.Name, "host": v.Host, "port": v.Port, "username": v.Username, "tls": v.TLS, "authSource": v.AuthSource, "hasPassword": v.PasswordEnc != ""}
		if v.InstanceID != "" {
			instance, ok := instances[v.InstanceID]
			if !ok {
				continue
			}
			item["instanceId"], item["version"], item["running"] = instance.ID, instance.Version, instance.Running
		} else if v.IsLocal && !installed {
			continue
		}
		out = append(out, item)
	}
	response.OK(c, out)
}
func (m *Module) saveNoSQLConnection(c *gin.Context) {
	var in struct {
		ID         string  `json:"id"`
		Name       string  `json:"name"`
		Host       string  `json:"host"`
		Port       int     `json:"port"`
		Username   string  `json:"username"`
		Password   *string `json:"password"`
		TLS        bool    `json:"tls"`
		AuthSource string  `json:"authSource"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || len([]rune(in.Name)) > 64 || in.Host == "" || strings.ContainsAny(in.Host, "/\r\n\t") || in.Port < 1 || in.Port > 65535 {
		response.Fail(c, response.Invalid, "连接名称、地址或端口无效")
		return
	}
	v := noSQLConnection{ID: in.ID, Name: in.Name, Host: in.Host, Port: in.Port, Username: in.Username, TLS: in.TLS, AuthSource: in.AuthSource}
	if v.AuthSource == "" {
		v.AuthSource = "admin"
	}
	engine := c.Param("engine")
	var previous noSQLConnection
	if v.ID == "" {
		v.ID = store.ID()
	} else {
		var old store.Setting
		if err := m.db.First(&old, "key = ?", "database."+engine+"."+v.ID).Error; err != nil {
			failure(c, err)
			return
		}
		if err := json.Unmarshal([]byte(old.Value), &previous); err != nil {
			failure(c, err)
			return
		}
		if previous.IsLocal {
			// 本机只维护认证，不允许将自动实例改成手工远程连接。
			v = previous
			v.Username = strings.TrimSpace(in.Username)
		}
		v.PasswordEnc = previous.PasswordEnc
	}
	if engine == "redis" && v.IsLocal {
		if v.Username != "" && v.Username != "default" {
			response.Fail(c, response.Invalid, "本机 Redis 仅支持默认用户，用户名请留空或填写 default")
			return
		}
		if in.Password != nil && strings.ContainsAny(*in.Password, "\x00\r\n") {
			response.Fail(c, response.Invalid, "本机 Redis 密码不能含空字符或换行")
			return
		}
	}
	if in.Password != nil {
		v.PasswordEnc = ""
		if *in.Password != "" {
			var err error
			v.PasswordEnc, err = store.Encrypt(m.cfg.AESKey, *in.Password)
			if err != nil {
				failure(c, err)
				return
			}
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		failure(c, err)
		return
	}
	ctx := c.Request.Context()
	save := func() error {
		return m.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&store.Setting{Key: "database." + engine + "." + v.ID, Value: string(b)}).Error
	}
	if engine == "redis" && v.IsLocal && in.Password != nil {
		err = m.packages.ApplicationChange("redis", func() error {
			return m.syncLocalRedisPassword(ctx, previous, *in.Password, save)
		})
	} else {
		err = save()
	}
	if err != nil {
		failure(c, err)
		return
	}
	response.OK(c, gin.H{"id": v.ID})
}

func (m *Module) revealNoSQLConnectionPassword(c *gin.Context) {
	var row store.Setting
	if err := m.db.First(&row, "key = ?", "database."+c.Param("engine")+"."+c.Param("connection")).Error; err != nil {
		failure(c, err)
		return
	}
	var v noSQLConnection
	if err := json.Unmarshal([]byte(row.Value), &v); err != nil {
		failure(c, err)
		return
	}
	password := ""
	if v.PasswordEnc != "" {
		var err error
		password, err = store.Decrypt(m.cfg.AESKey, v.PasswordEnc)
		if err != nil {
			failure(c, errors.New("无法读取连接密码"))
			return
		}
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, gin.H{"password": password})
}

func (m *Module) syncLocalRedisPassword(ctx context.Context, current noSQLConnection, newPassword string, save func() error) error {
	instanceID := current.InstanceID
	if instanceID == "" {
		instanceID = "default"
	}
	instance, err := m.packages.DatabaseInstance(ctx, "redis", instanceID)
	if err != nil {
		return fmt.Errorf("读取 Redis 实例状态失败：%w", err)
	}
	if instance.ContainerID == "" {
		return errors.New("Redis 实例不存在")
	}
	switch instance.Status {
	case "running", "created", "exited":
	default:
		return errors.New("Redis 实例状态暂不支持改密，请稍后重试")
	}
	diskPassword, err := m.packages.DatabaseInstancePassword("redis", instanceID)
	if err != nil {
		return fmt.Errorf("读取 Redis 启动密码文件失败：%w", err)
	}
	writePassword := func(password string) error {
		return m.packages.SetDatabaseInstancePassword("redis", instanceID, password)
	}
	if !instance.Running {
		return saveRedisPassword(newPassword, diskPassword, writePassword, save)
	}
	if instance.Port == 0 {
		return errors.New("Redis 实例没有本机连接端口")
	}
	target := noSQLConnection{Host: "127.0.0.1", Port: instance.Port}
	candidates := []string{diskPassword}
	if current.PasswordEnc != "" {
		if plain, err := store.Decrypt(m.cfg.AESKey, current.PasswordEnc); err == nil && !slices.Contains(candidates, plain) {
			candidates = append(candidates, plain)
		}
	}
	if !slices.Contains(candidates, "") {
		candidates = append(candidates, "")
	}
	for _, password := range candidates {
		var oldPassword string
		attempted := false
		err = withRedisConnection(ctx, target, password, "0", func(r *redisConnection) error {
			value, err := r.cmd("CONFIG", "GET", "requirepass")
			if err != nil {
				return err
			}
			values, ok := value.([]any)
			if !ok || len(values) != 2 {
				return errors.New("Redis 未返回当前密码配置")
			}
			oldPassword, ok = values[1].(string)
			if !ok {
				return errors.New("Redis 当前密码配置无效")
			}
			attempted = true
			_, err = r.cmd("CONFIG", "SET", "requirepass", newPassword)
			return err
		})
		if !attempted {
			continue
		}
		if err == nil {
			err = saveRedisPassword(newPassword, diskPassword, writePassword, save)
			if err == nil {
				return nil
			}
		} else {
			var reply redisReplyError
			if errors.As(err, &reply) {
				return errors.New("Redis 拒绝更新密码配置")
			}
			err = errors.New("未能确认 Redis 密码更新结果")
		}
		// 请求已断开时也要完成回滚。先用新密码连接，再尝试修改前的凭据。
		restore := func(credential string) error {
			return withRedisConnection(context.WithoutCancel(ctx), target, credential, "0", func(r *redisConnection) error {
				_, err := r.cmd("CONFIG", "SET", "requirepass", oldPassword)
				return err
			})
		}
		if restore(newPassword) != nil && (newPassword == password || restore(password) != nil) {
			err = errors.Join(err, errors.New("恢复 Redis 运行密码失败，请核对实例凭据"))
		}
		return err
	}
	return errors.New("无法验证 Redis 当前密码或读取密码配置")
}

// 启动密码文件必须保留 bind mount 的 inode，因此写入失败也要恢复旧内容。
func saveRedisPassword(newPassword, oldPassword string, write func(string) error, save func() error) error {
	err := write(newPassword)
	if err != nil {
		err = fmt.Errorf("保存 Redis 启动密码文件失败：%w", err)
	} else if err = save(); err != nil {
		err = fmt.Errorf("保存 Redis 面板凭据失败：%w", err)
	} else {
		return nil
	}
	if restoreErr := write(oldPassword); restoreErr != nil {
		return errors.Join(err, fmt.Errorf("恢复 Redis 启动密码文件失败：%w", restoreErr))
	}
	return err
}
func (m *Module) deleteNoSQLConnection(c *gin.Context) {
	v, _, err := m.noSQLConnection(c)
	if err != nil {
		failure(c, err)
		return
	}
	if v.IsLocal {
		response.Fail(c, response.Invalid, "本机连接自动管理，不能删除")
		return
	}
	engine := c.Param("engine")
	release, err := m.tasks.Acquire(LockKey(engine + ":" + v.ID))
	if err != nil {
		failure(c, err)
		return
	}
	defer release()
	err = m.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&store.Setting{}, "key = ?", "database."+engine+"."+v.ID).Error; err != nil {
			return err
		}
		if engine == "mongodb" {
			if err := tx.Where("connection_id = ?", v.ID).Delete(&store.MongoMetadata{}).Error; err != nil {
				return err
			}
			if err := tx.Where("engine = ? AND server_id = ?", "mongodb", v.ID).Delete(&databaseRecord{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		failure(c, err)
		return
	}
	response.OK(c, nil)
}
func (m *Module) noSQLConnection(c *gin.Context) (noSQLConnection, string, error) {
	var row store.Setting
	var v noSQLConnection
	if err := m.db.First(&row, "key = ?", "database."+c.Param("engine")+"."+c.Param("connection")).Error; err != nil {
		return v, "", err
	}
	if err := json.Unmarshal([]byte(row.Value), &v); err != nil {
		return v, "", err
	}
	if v.InstanceID != "" {
		if _, err := m.packages.RunningDatabaseInstance(c.Request.Context(), c.Param("engine"), v.InstanceID, v.Port); err != nil {
			return v, "", err
		}
	}
	password := ""
	if v.PasswordEnc != "" {
		var err error
		password, err = store.Decrypt(m.cfg.AESKey, v.PasswordEnc)
		if err != nil {
			return v, "", err
		}
	}
	return v, password, nil
}
func (m *Module) withMongo(c *gin.Context, fn func(context.Context, *mongo.Client) error) {
	if c.Param("engine") != "mongodb" {
		response.Fail(c, response.Invalid, "仅用于MongoDB")
		return
	}
	v, password, err := m.noSQLConnection(c)
	if err != nil {
		failure(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	client, err := connectMongo(ctx, v, password)
	if err != nil {
		failure(c, err)
		return
	}
	defer client.Disconnect(context.WithoutCancel(ctx))
	if err = fn(ctx, client); err != nil {
		failure(c, errors.New("MongoDB操作失败："+response.Summary(err.Error(), password)))
		return
	}
}
func (m *Module) noSQLDatabases(c *gin.Context) {
	if c.Param("engine") == "redis" {
		m.redisDatabases(c)
		return
	}
	m.withMongo(c, func(ctx context.Context, client *mongo.Client) error {
		result, err := client.ListDatabases(ctx, bson.D{})
		if err != nil {
			return err
		}
		var records []databaseRecord
		if err := m.db.Where("engine = ? AND server_id = ?", "mongodb", c.Param("connection")).Find(&records).Error; err != nil {
			return err
		}
		var metadata []store.MongoMetadata
		if err := m.db.Where("connection_id = ?", c.Param("connection")).Find(&metadata).Error; err != nil {
			return err
		}
		notes := map[string]store.MongoMetadata{}
		for _, row := range metadata {
			notes[row.Name] = row
		}
		var backups []struct {
			Database string
			Count    int64
		}
		if err := m.db.Model(&store.MongoBackup{}).Select("\"database\",count(*) AS count").Where("connection_id = ?", c.Param("connection")).Group("database").Scan(&backups).Error; err != nil {
			return err
		}
		counts := map[string]int64{}
		for _, row := range backups {
			counts[row.Database] = row.Count
		}
		stamps := map[string]time.Time{}
		for _, record := range records {
			stamps[record.Name] = record.CreatedAt
		}
		out := []gin.H{}
		for _, db := range result.Databases {
			if db.Name == "admin" || db.Name == "local" || db.Name == "config" {
				continue
			}
			row := gin.H{"name": db.Name, "size": db.SizeOnDisk, "system": false}
			users, userErr := mongoUsers(ctx, client, db.Name)
			note := notes[db.Name]
			username := note.Username
			recorded := slices.Contains(users, username) && username != "" && note.PasswordEnc != ""
			if !slices.Contains(users, username) {
				username = ""
				if len(users) == 1 {
					username = users[0]
				}
			}
			row["users"], row["username"], row["hasPassword"], row["remark"], row["backupCount"] = users, username, recorded, note.Remark, counts[db.Name]
			if userErr != nil {
				row["userNotice"] = "当前连接无权读取此库用户，请使用具备查看用户权限的账号"
			}
			if stamp, ok := stamps[db.Name]; ok {
				row["createdAt"] = stamp
			}
			out = append(out, row)
		}
		response.OK(c, out)
		return nil
	})
}
func (m *Module) createNoSQLDatabase(c *gin.Context) {
	if c.Param("engine") != "mongodb" {
		response.Fail(c, response.Invalid, "Redis逻辑库由服务配置确定")
		return
	}
	var in struct {
		Name       string `json:"name"`
		Collection string `json:"collection"`
		Username   string `json:"username"`
		Password   string `json:"password"`
	}
	if !response.Bind(c, &in) {
		return
	}
	autoUser := in.Collection == "" && in.Username == "" && in.Password == ""
	if in.Collection == "" {
		in.Collection = "init"
	}
	if !mongoDatabaseName.MatchString(in.Name) || !mongoCollectionName.MatchString(in.Collection) || in.Name == "admin" || in.Name == "local" || in.Name == "config" {
		response.Fail(c, response.Invalid, "数据库或初始集合名称无效")
		return
	}
	if autoUser {
		in.Username = in.Name
		in.Password = rand.Text()
	}
	if (in.Username == "") != (in.Password == "") || len(in.Username) > 128 || strings.ContainsRune(in.Username, 0) || len(in.Password) > 4096 || strings.ContainsRune(in.Password, 0) {
		response.Fail(c, response.Invalid, "专属用户名和密码需同时填写，用户名最多128字节")
		return
	}
	release, err := m.tasks.Acquire(LockKey("mongodb:" + c.Param("connection")))
	if err != nil {
		failure(c, err)
		return
	}
	defer release()
	m.withMongo(c, func(ctx context.Context, client *mongo.Client) error {
		existing, err := client.ListDatabaseNames(ctx, bson.D{{Key: "name", Value: in.Name}})
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			return errors.New("数据库已存在，请直接管理其集合")
		}

		if err := client.Database(in.Name).CreateCollection(ctx, in.Collection); err != nil {
			return err
		}
		if in.Username != "" {
			if err := m.mongoCreateAccount(ctx, client, c.Param("connection"), in.Name, in.Username, in.Password); err != nil {
				return fmt.Errorf("数据库已创建，%w；可在数据库列表补建账号", err)
			}
		}
		record := databaseRecord{Engine: "mongodb", ServerID: c.Param("connection"), Name: in.Name, CreatedAt: time.Now().UTC()}
		if err := m.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&record).Error; err != nil {
			return errors.New("数据库已创建，但创建时间保存失败，请刷新列表")
		}
		response.OK(c, nil)
		return nil
	})
}
func (m *Module) dropNoSQLDatabase(c *gin.Context) {
	name := c.Param("database")
	var in struct {
		ConfirmName  string `json:"confirmName"`
		Confirmation string `json:"confirmation"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Confirmation != "删除数据库" || in.ConfirmName != name || !mongoDatabaseName.MatchString(name) || name == "admin" || name == "local" || name == "config" {
		response.Fail(c, response.Invalid, "请确认可删除的数据库名称")
		return
	}
	if c.Param("engine") == "redis" {
		response.Fail(c, response.Invalid, "Redis不支持删除逻辑库")
		return
	}
	release, err := m.tasks.Acquire(LockKey("mongodb:" + c.Param("connection")))
	if err != nil {
		failure(c, err)
		return
	}
	defer release()
	m.withMongo(c, func(ctx context.Context, client *mongo.Client) error {
		if err := client.Database(name).Drop(ctx); err != nil {
			return err
		}
		if err := m.db.Where("engine = ? AND server_id = ? AND name = ?", "mongodb", c.Param("connection"), name).Delete(&databaseRecord{}).Error; err != nil {
			return errors.New("数据库已删除，但面板记录更新失败")
		}
		if err := m.db.Where("connection_id = ? AND name = ?", c.Param("connection"), name).Delete(&store.MongoMetadata{}).Error; err != nil {
			return errors.New("数据库已删除，但面板备注清理失败")
		}
		response.OK(c, nil)
		return nil
	})
}
func mongoTarget(c *gin.Context) (string, string, bool) {
	db, collection := c.Query("database"), c.Query("collection")
	if !mongoDatabaseName.MatchString(db) || (collection != "" && !mongoCollectionName.MatchString(collection)) {
		response.Fail(c, response.Invalid, "数据库或集合名称无效")
		return "", "", false
	}
	return db, collection, true
}

type mongoCollection struct {
	Name      string   `json:"name"`
	Count     *int64   `json:"count"`
	Size      *float64 `json:"size"`
	Average   *float64 `json:"average"`
	Storage   *float64 `json:"storage"`
	Indexes   *int64   `json:"indexes"`
	IndexSize *float64 `json:"indexSize"`
	Notice    string   `json:"notice"`
}

func (m *Module) mongoCollections(c *gin.Context) {
	db, _, ok := mongoTarget(c)
	if !ok {
		return
	}
	page, size, ok := response.Page(c, 100, 10)
	if !ok {
		return
	}
	m.withMongo(c, func(ctx context.Context, client *mongo.Client) error {
		names, err := client.Database(db).ListCollectionNames(ctx, bson.D{})
		if err != nil {
			return err
		}
		sort.Strings(names)
		start := min((page-1)*size, len(names))
		end := min(start+size, len(names))
		out := []mongoCollection{}
		for _, name := range names[start:end] {
			row := mongoCollection{Name: name}
			cursor, err := client.Database(db).Collection(name).Aggregate(ctx, mongo.Pipeline{
				bson.D{{Key: "$collStats", Value: bson.D{{Key: "storageStats", Value: bson.D{}}}}},
				bson.D{{Key: "$project", Value: bson.D{{Key: "count", Value: "$storageStats.count"}, {Key: "size", Value: "$storageStats.size"}, {Key: "average", Value: "$storageStats.avgObjSize"}, {Key: "storage", Value: "$storageStats.storageSize"}, {Key: "indexes", Value: "$storageStats.nindexes"}, {Key: "indexSize", Value: "$storageStats.totalIndexSize"}}}},
			})
			if err != nil {
				row.Notice = "统计不可用，请检查集合类型或统计权限"
			} else {
				// 分片集合可能返回多条统计，按文档数加权平均对象大小。
				var count, indexes int64
				var logical, storage, indexSize float64
				for cursor.Next(ctx) {
					var stats struct {
						Count     int64   `bson:"count"`
						Size      float64 `bson:"size"`
						Storage   float64 `bson:"storage"`
						Indexes   int64   `bson:"indexes"`
						IndexSize float64 `bson:"indexSize"`
					}
					if err = cursor.Decode(&stats); err != nil {
						break
					}
					count += stats.Count
					logical += stats.Size
					storage += stats.Storage
					indexes = max(indexes, stats.Indexes)
					indexSize += stats.IndexSize
				}
				if err == nil {
					err = cursor.Err()
				}
				cursor.Close(ctx)
				if err != nil {
					row.Notice = "集合统计读取失败"
				} else {
					average := float64(0)
					if count > 0 {
						average = logical / float64(count)
					}
					row.Count = &count
					row.Size = &logical
					row.Average = &average
					row.Storage = &storage
					row.Indexes = &indexes
					row.IndexSize = &indexSize
				}
			}
			out = append(out, row)
		}
		response.OK(c, gin.H{"list": out, "total": len(names)})
		return nil
	})
}
func (m *Module) createMongoCollection(c *gin.Context) {
	db, _, ok := mongoTarget(c)
	if !ok {
		return
	}
	var in struct {
		Name     string          `json:"name"`
		Document json.RawMessage `json:"document"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if db == "admin" || db == "local" || db == "config" || strings.HasPrefix(in.Name, "system.") || !mongoCollectionName.MatchString(in.Name) {
		response.Fail(c, response.Invalid, "集合名称无效")
		return
	}
	var document bson.D
	if len(in.Document) > 0 && bson.UnmarshalExtJSON(in.Document, false, &document) != nil {
		response.Fail(c, response.Invalid, "初始文档须为合法Extended JSON对象")
		return
	}
	m.withMongo(c, func(ctx context.Context, client *mongo.Client) error {
		if err := client.Database(db).CreateCollection(ctx, in.Name); err != nil {
			return err
		}
		if len(in.Document) > 0 {
			if _, err := client.Database(db).Collection(in.Name).InsertOne(ctx, document); err != nil {
				if cleanup := client.Database(db).Collection(in.Name).Drop(ctx); cleanup != nil {
					return errors.New("初始文档创建失败，新集合清理失败，请刷新后检查")
				}
				return errors.New("初始文档创建失败，空集合已清理")
			}
		}
		response.OK(c, nil)
		return nil
	})
}
func (m *Module) mongoDocuments(c *gin.Context) {
	db, collection, ok := mongoTarget(c)
	if !ok {
		return
	}
	if collection == "" {
		response.Fail(c, response.Invalid, "请选择集合")
		return
	}
	page, size, ok := response.Page(c, 100, 10)
	if !ok {
		return
	}
	m.withMongo(c, func(ctx context.Context, client *mongo.Client) error {
		total, err := client.Database(db).Collection(collection).CountDocuments(ctx, bson.D{})
		if err != nil {
			return err
		}
		cursor, err := client.Database(db).Collection(collection).Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetSkip(int64((page-1)*size)).SetLimit(int64(size)).SetBatchSize(10))
		if err != nil {
			return err
		}
		defer cursor.Close(ctx)
		out := []json.RawMessage{}
		size, truncated := 0, false
		for cursor.Next(ctx) {
			raw, err := bson.MarshalExtJSON(cursor.Current, true, false)
			if err != nil {
				return err
			}
			if size+len(raw) > 2<<20 {
				truncated = true
				break
			}
			size += len(raw)
			out = append(out, json.RawMessage(raw))
		}
		if err = cursor.Err(); err != nil {
			return err
		}
		response.OK(c, gin.H{"list": out, "truncated": truncated, "total": total})
		return nil
	})
}
func (m *Module) insertMongoDocument(c *gin.Context) {
	db, collection, ok := mongoTarget(c)
	if !ok {
		return
	}
	if collection == "" || strings.HasPrefix(collection, "system.") || db == "admin" || db == "local" || db == "config" {
		response.Fail(c, response.Invalid, "请选择业务集合")
		return
	}
	var in struct {
		Document json.RawMessage `json:"document"`
	}
	if !response.Bind(c, &in) {
		return
	}
	var document bson.D
	if err := bson.UnmarshalExtJSON(in.Document, false, &document); err != nil {
		response.Fail(c, response.Invalid, "文档须为合法MongoDB Extended JSON对象")
		return
	}
	m.withMongo(c, func(ctx context.Context, client *mongo.Client) error {
		out, err := client.Database(db).Collection(collection).InsertOne(ctx, document)
		if err != nil {
			return err
		}
		response.OK(c, out)
		return nil
	})
}

func (m *Module) mongoRootPassword(c *gin.Context) {
	if c.Param("engine") != "mongodb" {
		response.Fail(c, response.Invalid, "仅用于MongoDB")
		return
	}
	v, password, err := m.noSQLConnection(c)
	if err != nil {
		failure(c, err)
		return
	}
	if c.Request.Method == "POST" {
		if v.Username != "root" {
			password = ""
		}
		c.Header("Cache-Control", "no-store")
		response.OK(c, gin.H{"password": password})
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Password == "" || len(in.Password) > 1024 || strings.ContainsRune(in.Password, 0) {
		response.Fail(c, response.Invalid, "请输入有效的新密码")
		return
	}
	encrypted, err := store.Encrypt(m.cfg.AESKey, in.Password)
	if err != nil {
		failure(c, err)
		return
	}
	release, err := m.tasks.Acquire(LockKey("mongodb:" + v.ID))
	if err != nil {
		failure(c, err)
		return
	}
	defer release()
	m.withMongo(c, func(ctx context.Context, client *mongo.Client) error {
		admin := client.Database("admin")
		var result struct {
			Users []bson.M `bson:"users"`
		}
		if err := admin.RunCommand(ctx, bson.D{{Key: "usersInfo", Value: "root"}}).Decode(&result); err != nil {
			return err
		}
		command := bson.D{{Key: "updateUser", Value: "root"}, {Key: "pwd", Value: in.Password}}
		if len(result.Users) == 0 {
			command = bson.D{{Key: "createUser", Value: "root"}, {Key: "pwd", Value: in.Password}, {Key: "roles", Value: bson.A{bson.D{{Key: "role", Value: "root"}, {Key: "db", Value: "admin"}}}}}
		}
		if err := admin.RunCommand(ctx, command).Err(); err != nil {
			return errors.New("MongoDB管理员密码修改失败，请检查权限与认证设置")
		}
		v.Username = "root"
		v.AuthSource = "admin"
		v.PasswordEnc = encrypted
		raw, _ := json.Marshal(v)
		if err := m.db.Model(&store.Setting{}).Where("key = ?", "database.mongodb."+v.ID).Update("value", string(raw)).Error; err != nil {
			return errors.New("MongoDB密码已更新，但面板凭据保存失败")
		}
		response.OK(c, nil)
		return nil
	})
}
