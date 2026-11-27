package database

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"ezpanel/internal/auth"
	"ezpanel/internal/executor"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func connectMongo(ctx context.Context, v noSQLConnection, password string) (*mongo.Client, error) {
	opts := options.Client().SetHosts([]string{net.JoinHostPort(v.Host, strconv.Itoa(v.Port))}).SetServerSelectionTimeout(5 * time.Second).SetConnectTimeout(5 * time.Second)
	if v.Username != "" {
		opts.SetAuth(options.Credential{Username: v.Username, Password: password, AuthSource: v.AuthSource})
	}
	if v.TLS {
		opts.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: v.Host})
	}
	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, errors.New("MongoDB连接配置失败")
	}
	if err = client.Ping(ctx, nil); err != nil {
		client.Disconnect(context.WithoutCancel(ctx))
		return nil, errors.New("MongoDB连接失败，请检查地址、认证、TLS和访问权限")
	}
	return client, nil
}

// 读取本库真实用户；不把管理员连接账号当作每个数据库的专属用户。
func mongoUsers(ctx context.Context, client *mongo.Client, database string) ([]string, error) {
	var result struct {
		Users []struct {
			User string `bson:"user"`
		} `bson:"users"`
	}
	err := client.Database(database).RunCommand(ctx, bson.D{{Key: "usersInfo", Value: 1}}).Decode(&result)
	users := []string{}
	for _, user := range result.Users {
		users = append(users, user.User)
	}
	slices.Sort(users)
	return users, err
}
func mongoBusinessDatabase(name string) bool {
	return mongoDatabaseName.MatchString(name) && name != "admin" && name != "local" && name != "config"
}
func (m *Module) mongoMetadata(c *gin.Context) {
	if c.Param("engine") != "mongodb" || !mongoBusinessDatabase(c.Param("database")) {
		response.Fail(c, response.Invalid, "数据库名称无效")
		return
	}
	if _, _, err := m.noSQLConnection(c); err != nil {
		failure(c, err)
		return
	}
	var in struct {
		Remark string `json:"remark"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if !utf8.ValidString(in.Remark) || utf8.RuneCountInString(in.Remark) > 500 || strings.ContainsRune(in.Remark, 0) {
		response.Fail(c, response.Invalid, "备注最多500字，不能包含空字符")
		return
	}
	release, err := m.tasks.Acquire(LockKey("mongodb:" + c.Param("connection")))
	if err != nil {
		failure(c, err)
		return
	}
	defer release()
	row := store.MongoMetadata{ConnectionID: c.Param("connection"), Name: c.Param("database"), Remark: strings.TrimSpace(in.Remark)}
	if err = m.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "connection_id"}, {Name: "name"}}, DoUpdates: clause.AssignmentColumns([]string{"remark"})}).Create(&row).Error; err != nil {
		failure(c, err)
		return
	}
	response.OK(c, nil)
}
func (m *Module) revealMongoPassword(c *gin.Context) {
	if !mongoBusinessDatabase(c.Param("database")) {
		response.Fail(c, response.Invalid, "数据库名称无效")
		return
	}
	m.withMongo(c, func(ctx context.Context, client *mongo.Client) error {
		var row store.MongoMetadata
		if err := m.db.First(&row, "connection_id = ? AND name = ?", c.Param("connection"), c.Param("database")).Error; err != nil {
			return errors.New("未记录此库专属账号密码")
		}
		users, err := mongoUsers(ctx, client, row.Name)
		if err != nil {
			return err
		}
		if row.Username == "" || row.PasswordEnc == "" || !slices.Contains(users, row.Username) {
			return errors.New("未记录此库现有账号的密码")
		}
		password, err := store.Decrypt(m.cfg.AESKey, row.PasswordEnc)
		if err != nil {
			return err
		}
		c.Header("Cache-Control", "no-store")
		response.OK(c, gin.H{"password": password})
		return nil
	})
}
func (m *Module) createMongoDatabaseAccount(c *gin.Context) {
	name, connection := c.Param("database"), c.Param("connection")
	if c.Param("engine") != "mongodb" || !mongoBusinessDatabase(name) {
		response.Fail(c, response.Invalid, "数据库名称无效")
		return
	}
	var in struct{ Username, Password string }
	if !response.Bind(c, &in) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" || len(in.Username) > 128 || strings.ContainsRune(in.Username, 0) ||
		in.Password == "" || len(in.Password) > 4096 || strings.ContainsRune(in.Password, 0) {
		response.Fail(c, response.Invalid, "请输入有效的专属账号和密码")
		return
	}
	release, err := m.tasks.Acquire(LockKey("mongodb:" + connection))
	if err != nil {
		failure(c, err)
		return
	}
	defer release()
	m.withMongo(c, func(ctx context.Context, client *mongo.Client) error {
		names, err := client.ListDatabaseNames(ctx, bson.D{{Key: "name", Value: name}})
		if err != nil {
			return err
		}
		if !slices.Contains(names, name) {
			return gorm.ErrRecordNotFound
		}
		users, err := mongoUsers(ctx, client, name)
		if err != nil {
			return err
		}
		if len(users) > 0 {
			return errors.New("此库已有独立账号，请在列表中使用改密")
		}
		if err := m.mongoCreateAccount(ctx, client, connection, name, in.Username, in.Password); err != nil {
			return err
		}
		response.OK(c, nil)
		return nil
	})
}

func (m *Module) mongoCreateAccount(ctx context.Context, client *mongo.Client, connection, name, username, password string) error {
	encrypted, err := store.Encrypt(m.cfg.AESKey, password)
	if err != nil {
		return err
	}
	if err := client.Database(name).RunCommand(ctx, bson.D{
		{Key: "createUser", Value: username}, {Key: "pwd", Value: password},
		{Key: "roles", Value: bson.A{bson.D{{Key: "role", Value: "readWrite"}, {Key: "db", Value: name}}}},
	}).Err(); err != nil {
		return errors.New("专属账号创建失败，请检查用户管理权限")
	}
	row := store.MongoMetadata{ConnectionID: connection, Name: name, Username: username, PasswordEnc: encrypted}
	if err := m.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "connection_id"}, {Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"username", "password_enc"}),
	}).Create(&row).Error; err != nil {
		return errors.New("专属账号已创建，但面板密码记录保存失败")
	}
	return nil
}

func (m *Module) mongoChangePassword(ctx context.Context, client *mongo.Client, connection, name, username, password string) error {
	users, err := mongoUsers(ctx, client, name)
	if err != nil {
		return err
	}
	if username == "" {
		var note store.MongoMetadata
		err = m.db.First(&note, "connection_id = ? AND name = ?", connection, name).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if slices.Contains(users, note.Username) {
			username = note.Username
		} else if len(users) == 1 {
			username = users[0]
		}
	}
	if username == "" || !slices.Contains(users, username) {
		return errors.New("请选择此库实际存在的用户；多个用户时请单独修改")
	}
	encrypted, err := store.Encrypt(m.cfg.AESKey, password)
	if err != nil {
		return err
	}
	if err = client.Database(name).RunCommand(ctx, bson.D{{Key: "updateUser", Value: username}, {Key: "pwd", Value: password}}).Err(); err != nil {
		return errors.New("账号密码修改失败，请检查用户管理权限")
	}
	row := store.MongoMetadata{ConnectionID: connection, Name: name, Username: username, PasswordEnc: encrypted}
	if err = m.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "connection_id"}, {Name: "name"}}, DoUpdates: clause.AssignmentColumns([]string{"username", "password_enc"})}).Create(&row).Error; err != nil {
		return errors.New("账号密码已修改，但面板密码记录保存失败")
	}
	return nil
}

func (m *Module) mongoBackup(ctx context.Context, v noSQLConnection, password, name string) error {
	container, host, port := "ezpanel-mongodb", v.Host, v.Port
	if v.InstanceID != "" {
		instance, err := m.packages.RunningDatabaseInstance(ctx, "mongodb", v.InstanceID, v.Port)
		if err != nil {
			return err
		}
		container, host, port = instance.ContainerID, "127.0.0.1", 27017
	}
	safeName := pathsafe.BackupName(name)
	dir := filepath.Join(m.cfg.Current().BackupRoot, "database", "mongodb", v.InstanceID, safeName)
	policy, err := pathsafe.New(m.cfg.InstallDir)
	if err != nil {
		return err
	}
	if _, err = policy.Check(dir, true); err != nil {
		return errors.New("数据库备份目录不可用或指向受保护目录")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(map[string]string{"password": password})
	if err != nil {
		return err
	}
	id := store.ID()
	location, err := time.LoadLocation(m.cfg.Current().Timezone)
	if err != nil {
		location = time.UTC
	}
	filename := safeName + "_mongodb_data_" + time.Now().In(location).Format("20060102") + "_" + strconv.Itoa(100000+rand.IntN(900000)) + ".archive.gz"
	temp, err := os.CreateTemp(dir, ".mongo-dump-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	args := []string{"--host", host, "--port", strconv.Itoa(port), "--db", name, "--archive", "--gzip"}
	if v.Username != "" {
		args = append(args, "--username", v.Username, "--authenticationDatabase", v.AuthSource)
	}
	if v.TLS {
		args = append(args, "--ssl")
	}
	path, lookErr := executor.LookPath("mongodump")
	if v.IsLocal {
		path, lookErr = executor.LookPath("docker")
		script := `file=$(mktemp) || exit 1
trap 'rm -f "$file"' EXIT
cat > "$file" || exit 1
mongodump --config="$file" "$@"`
		args = append([]string{"exec", "-i", container, "sh", "-c", script, "mongodump"}, args...)
	} else if lookErr == nil && v.Username != "" {
		credentials, fileErr := os.CreateTemp(dir, ".mongo-credentials-*")
		if fileErr != nil {
			temp.Close()
			return fileErr
		}
		defer os.Remove(credentials.Name())
		_, fileErr = credentials.Write(raw)
		if err := credentials.Close(); err != nil || fileErr != nil {
			temp.Close()
			return errors.Join(fileErr, err)
		}
		args = append(args, "--config", credentials.Name())
	}
	if lookErr != nil {
		temp.Close()
		return errors.New("缺少 MongoDB 备份客户端")
	}
	_, err = executor.Pipe(ctx, time.Hour, nil, strings.NewReader(string(raw)), temp, path, args...)
	closeErr := temp.Close()
	if err != nil {
		return errors.New("mongodump备份失败，请检查工具版本、连接认证及备份权限")
	}
	if closeErr != nil {
		return closeErr
	}
	info, err := os.Stat(temp.Name())
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return errors.New("mongodump未生成有效备份文件")
	}
	final := filepath.Join(dir, filename)
	if err = os.Link(temp.Name(), final); err != nil {
		return err
	}
	row := store.MongoBackup{BackupFile: store.BackupFile{FileName: filename, StorageType: "local", Method: "manual"}, ID: id, ConnectionID: v.ID, ConnectionName: v.Name, Database: name, FilePath: final, Size: info.Size(), CreatedAt: time.Now().UTC()}
	if err = m.db.Create(&row).Error; err != nil {
		os.Remove(final)
		return errors.New("备份登记失败")
	}
	return nil
}

func (m *Module) mongoBatch(c *gin.Context) {
	if c.Param("engine") != "mongodb" {
		response.Fail(c, response.Invalid, "仅用于MongoDB")
		return
	}
	var in struct {
		Action       string   `json:"action"`
		Databases    []string `json:"databases"`
		Password     string   `json:"password"`
		Username     string   `json:"username"`
		Confirmation string   `json:"confirmation"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if len(in.Databases) < 1 || len(in.Databases) > 100 || (in.Action != "password" && in.Action != "backup" && in.Action != "delete") {
		response.Fail(c, response.Invalid, "请选择1至100个数据库及有效操作")
		return
	}
	for _, name := range in.Databases {
		if !mongoBusinessDatabase(name) {
			response.Fail(c, response.Invalid, "数据库名称无效或为系统数据库")
			return
		}
	}
	if in.Action == "password" && (!utf8.ValidString(in.Password) || len(in.Password) == 0 || utf8.RuneCountInString(in.Password) > 1024 || strings.ContainsRune(in.Password, 0)) {
		response.Fail(c, response.Invalid, "请输入1至1024字密码，不能包含空字符")
		return
	}
	if in.Action == "delete" && in.Confirmation != "删除数据库" {
		response.Fail(c, response.Invalid, "请输入删除数据库确认操作")
		return
	}
	v, password, err := m.noSQLConnection(c)
	if err != nil {
		failure(c, err)
		return
	}
	if in.Action == "backup" && !v.IsLocal {
		if _, err = executor.LookPath("mongodump"); err != nil {
			response.Fail(c, response.Forbidden, "请先在面板服务器安装 MongoDB Database Tools（mongodump，需支持 --config）")
			return
		}
	}
	session := auth.Current(c)
	id, err := m.tasks.Submit(task.Meta{Module: "database", Action: in.Action, Target: v.Name + "/MongoDB", ResourceID: v.ID, Username: session.User.Username, IP: m.auth.ClientIP(c), LockKey: LockKey("mongodb:" + v.ID)}, func(parent context.Context, log *task.Log) (any, error) {
		ctx, cancel := context.WithTimeout(parent, time.Hour)
		defer cancel()
		client, err := connectMongo(ctx, v, password)
		if err != nil {
			return nil, err
		}
		defer client.Disconnect(context.WithoutCancel(ctx))
		result := task.BatchResult{Results: []task.ItemResult{}}
		for _, name := range in.Databases {
			err = ctx.Err()
			if err == nil {
				switch in.Action {
				case "password":
					err = m.mongoChangePassword(ctx, client, v.ID, name, in.Username, in.Password)
				case "backup":
					err = m.mongoBackup(ctx, v, password, name)
				case "delete":
					err = client.Database(name).Drop(ctx)
					if err == nil {
						err = m.db.Where("connection_id = ? AND name = ?", v.ID, name).Delete(&store.MongoMetadata{}).Error
					}
					if err == nil {
						err = m.db.Where("engine = ? AND server_id = ? AND name = ?", "mongodb", v.ID, name).Delete(&databaseRecord{}).Error
					}
				}
			}
			item := task.ItemResult{Target: name, Msg: "操作完成"}
			if err != nil {
				item.Code = response.System
				item.Msg = response.Summary(err.Error(), password, in.Password)
				result.FailedCount++
			} else {
				result.SuccessCount++
			}
			result.Results = append(result.Results, item)
			log.Line(name + "：" + item.Msg)
		}
		if result.FailedCount > 0 {
			return result, errors.New("部分MongoDB操作失败，请查看逐项结果")
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
