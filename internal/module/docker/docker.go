package docker

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Module struct {
	db       *gorm.DB
	cfg      config.Config
	tasks    *task.Manager
	packages *pkgmgr.Manager
	auth     *auth.Service
	client   *http.Client
}

func New(db *gorm.DB, cfg config.Config, tasks *task.Manager, packages *pkgmgr.Manager, a *auth.Service) *Module {
	return &Module{db, cfg, tasks, packages, a, engineClient()}
}
func (m *Module) Name() string { return "docker" }

var resourceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,255}$`)

func fail(c *gin.Context, err error) {
	response.Fail(c, response.System, response.Summary(err.Error()))
}
func (m *Module) Routes(g *gin.RouterGroup) {
	r := g.Group("/api/docker")
	r.Use(func(c *gin.Context) {
		switch c.FullPath() {
		case "/api/docker/images/:id/archive", "/api/docker/compose/projects/:name/logs/stream":
			c.Next()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.GET("/overview", m.overview)
	r.GET("/containers", func(c *gin.Context) { m.read(c, "/containers/json?all=true") })
	r.POST("/containers", m.createContainer)
	r.GET("/containers/:id", func(c *gin.Context) {
		suffix := "/json"
		if c.Query("size") == "true" {
			suffix += "?size=true"
		}
		m.readResource(c, "containers", suffix)
	})
	r.GET("/containers/:id/stats", func(c *gin.Context) { m.readResource(c, "containers", "/stats?stream=false") })
	r.GET("/containers/:id/top", func(c *gin.Context) { m.readResource(c, "containers", "/top") })
	r.GET("/containers/:id/logs", m.logs)
	r.POST("/containers/:id/action", m.containerAction)
	r.POST("/containers/:id/exec", m.exec)
	r.POST("/containers/:id/update", m.updateContainer)
	r.POST("/containers/:id/recreate", m.recreateContainer)
	r.POST("/containers/:id/commit", m.commitContainer)
	r.DELETE("/containers/:id", m.removeContainer)
	r.GET("/images", func(c *gin.Context) { m.read(c, "/images/json?all=false") })
	r.GET("/images/search", m.search)
	r.GET("/images/:id/archive", m.imageArchive)
	r.POST("/images/pull", m.imageTransfer)
	r.POST("/images/push", m.imageTransfer)
	r.POST("/images/load", m.loadImage)
	r.POST("/images/build", m.buildImage)
	r.POST("/images/:id/tag", m.tag)
	r.DELETE("/images/:id", func(c *gin.Context) { m.remove(c, "images") })
	r.GET("/networks", func(c *gin.Context) { m.read(c, "/networks") })
	r.POST("/networks", m.createNetwork)
	r.GET("/networks/:id", func(c *gin.Context) { m.readResource(c, "networks", "") })
	r.POST("/networks/:id/connect", m.connectNetwork)
	r.DELETE("/networks/:id", func(c *gin.Context) { m.remove(c, "networks") })
	r.GET("/volumes", func(c *gin.Context) { m.read(c, "/volumes") })
	r.POST("/volumes", m.createVolume)
	r.GET("/volumes/:id", func(c *gin.Context) { m.readResource(c, "volumes", "") })
	r.DELETE("/volumes/:id", func(c *gin.Context) { m.remove(c, "volumes") })
	r.POST("/prune/:kind", m.prune)
	r.GET("/compose/projects", m.listComposeProjects)
	r.POST("/compose/projects", m.createComposeProject)
	r.POST("/compose/projects/validate", m.validateComposeProject)
	r.GET("/compose/projects/:name", m.composeProjectDetail)
	r.PUT("/compose/projects/:name/files", m.updateComposeFiles)
	r.POST("/compose/projects/:name/action", m.composeProjectAction)
	r.DELETE("/compose/projects/:name", m.deleteComposeProject)
	r.GET("/compose/projects/:name/logs", m.composeProjectLogs)
	r.GET("/compose/projects/:name/logs/stream", m.streamComposeProjectLogs)
	r.GET("/compose/projects/:name/revisions", m.listComposeRevisions)
	r.GET("/compose/projects/:name/revisions/:revision", m.composeRevisionDetail)
	r.POST("/compose/projects/:name/revisions/:revision/rollback", m.rollbackComposeRevision)
	r.GET("/compose/templates", m.composeTemplates)
	r.POST("/compose/templates", m.saveComposeTemplate)
	r.POST("/compose/templates/import", m.importComposeTemplate)
	r.POST("/compose/templates/from-project", m.saveComposeProjectTemplate)
	r.GET("/compose/templates/:id", m.composeTemplate)
	r.PUT("/compose/templates/:id", m.saveComposeTemplate)
	r.DELETE("/compose/templates/:id", m.deleteComposeTemplate)
	r.POST("/compose/templates/:id/deploy", m.deployComposeTemplate)
	r.GET("/compose/templates/:id/export", m.exportComposeTemplate)
	r.GET("/registries", m.registries)
	r.POST("/registries", m.saveRegistry)
	r.DELETE("/registries/:id", m.deleteRegistry)
	r.POST("/registries/:id/test", m.testRegistry)
}
func resource(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if !resourceName.MatchString(id) {
		response.Fail(c, response.Invalid, "资源标识无效")
		return "", false
	}
	return url.PathEscape(id), true
}
func (m *Module) read(c *gin.Context, path string) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	var out any
	if err := m.json(ctx, "GET", path, nil, &out); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, out)
}
func (m *Module) readResource(c *gin.Context, kind, suffix string) {
	if id, ok := resource(c); ok {
		m.read(c, "/"+kind+"/"+id+suffix)
	}
}
func (m *Module) overview(c *gin.Context) {
	state, err := m.packages.Status(c.Request.Context(), "docker")
	if err != nil {
		fail(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	var info, version, usage map[string]any
	if err = m.json(ctx, "GET", "/info", nil, &info); err != nil {
		response.OK(c, gin.H{"installed": state.Installed, "running": false, "notice": err.Error()})
		return
	}
	if err = m.json(ctx, "GET", "/version", nil, &version); err != nil {
		fail(c, err)
		return
	}
	if err = m.json(ctx, "GET", "/system/df", nil, &usage); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"installed": true, "running": true, "info": info, "version": version, "usage": usage})
}

type containerInput struct {
	Name    string   `json:"name"`
	Image   string   `json:"image"`
	Command []string `json:"command"`
	Env     []string `json:"env"`
	Ports   []struct {
		HostIP    string `json:"hostIp"`
		Host      int    `json:"host"`
		Container int    `json:"container"`
		Protocol  string `json:"protocol"`
	} `json:"ports"`
	Mounts    []containerMountInput `json:"mounts"`
	Network   string                `json:"network"`
	Restart   string                `json:"restart"`
	MemoryMB  int64                 `json:"memoryMB"`
	CPUs      float64               `json:"cpus"`
	AutoStart bool                  `json:"autoStart"`
}

func validImage(value string) bool {
	return value != "" && len(value) <= 512 && !strings.ContainsAny(value, " \t\r\n?&#")
}
func (m *Module) createContainer(c *gin.Context) {
	var in containerInput
	if !response.Bind(c, &in) {
		return
	}
	if !resourceName.MatchString(in.Name) || !validImage(in.Image) || in.MemoryMB < 0 || in.MemoryMB > 1048576 || in.CPUs < 0 || in.CPUs > 1024 {
		response.Fail(c, response.Invalid, "容器名称、镜像或资源限制无效")
		return
	}
	switch in.Restart {
	case "no", "always", "unless-stopped", "on-failure":
	default:
		response.Fail(c, response.Invalid, "重启策略无效")
		return
	}
	ports := map[string]any{}
	exposed := map[string]any{}
	for _, p := range in.Ports {
		if p.Host < 1 || p.Host > 65535 || p.Container < 1 || p.Container > 65535 || (p.Protocol != "tcp" && p.Protocol != "udp") {
			response.Fail(c, response.Invalid, "端口需为1–65535，协议为tcp/udp")
			return
		}
		key := strconv.Itoa(p.Container) + "/" + p.Protocol
		exposed[key] = gin.H{}
		if ports[key] == nil {
			ports[key] = []gin.H{}
		}
		ports[key] = append(ports[key].([]gin.H), gin.H{"HostIp": p.HostIP, "HostPort": strconv.Itoa(p.Host)})
	}
	mounts, err := containerMounts(in.Mounts)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	defer cancel()
	var out struct {
		ID       string   `json:"Id"`
		Warnings []string `json:"Warnings"`
	}
	body := gin.H{"Image": in.Image, "Cmd": in.Command, "Env": in.Env, "ExposedPorts": exposed, "HostConfig": gin.H{"PortBindings": ports, "Mounts": mounts, "NetworkMode": in.Network, "RestartPolicy": gin.H{"Name": in.Restart}, "Memory": in.MemoryMB * 1024 * 1024, "NanoCpus": int64(in.CPUs * 1e9)}}
	if err := m.json(ctx, "POST", "/containers/create?name="+url.QueryEscape(in.Name), body, &out); err != nil {
		fail(c, err)
		return
	}
	if in.AutoStart {
		if err := m.json(ctx, "POST", "/containers/"+out.ID+"/start", nil, nil); err != nil {
			response.Fail(c, response.System, "容器已创建但未启动："+response.Summary(err.Error()))
			return
		}
	}
	response.OK(c, gin.H{"id": out.ID, "warnings": out.Warnings})
}
func (m *Module) containerAction(c *gin.Context) {
	id, ok := resource(c)
	if !ok {
		return
	}
	var in struct {
		Action string `json:"action"`
		Name   string `json:"name"`
	}
	if !response.Bind(c, &in) {
		return
	}
	path := "/containers/" + id + "/" + in.Action
	switch in.Action {
	case "start", "stop", "restart", "pause", "unpause", "kill":
	case "rename":
		if !resourceName.MatchString(in.Name) {
			response.Fail(c, response.Invalid, "容器名无效")
			return
		}
		path += "?name=" + url.QueryEscape(in.Name)
	default:
		response.Fail(c, response.Invalid, "操作无效")
		return
	}
	release, err := m.tasks.Acquire("docker:container:" + id)
	if err != nil {
		fail(c, err)
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	defer cancel()
	if in.Action == "rename" {
		var item struct {
			Config struct{ Labels map[string]string }
		}
		if err := m.json(ctx, "GET", "/containers/"+id+"/json", nil, &item); err != nil {
			fail(c, err)
			return
		}
		if owner := containerConfigOwner(item.Config.Labels); owner != "" {
			response.Fail(c, response.Conflict, "请从"+owner+"管理此容器的配置")
			return
		}
	}
	if err = m.json(ctx, "POST", path, nil, nil); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, nil)
}
func (m *Module) updateContainer(c *gin.Context) {
	id, ok := resource(c)
	if !ok {
		return
	}
	var in struct {
		Restart  string   `json:"restart"`
		Retries  int      `json:"retries"`
		MemoryMB *int64   `json:"memoryMB"`
		CPUs     *float64 `json:"cpus"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if (in.MemoryMB != nil && (*in.MemoryMB < 0 || *in.MemoryMB > 1048576)) ||
		(in.CPUs != nil && (*in.CPUs < 0 || *in.CPUs > 1024)) || in.Retries < 0 || in.Retries > 1000 {
		response.Fail(c, response.Invalid, "资源限制无效")
		return
	}
	switch in.Restart {
	case "", "no", "always", "unless-stopped", "on-failure":
	default:
		response.Fail(c, response.Invalid, "重启策略无效")
		return
	}
	release, err := m.tasks.Acquire("docker:container:" + id)
	if err != nil {
		fail(c, err)
		return
	}
	defer release()
	ctx := c.Request.Context()
	body := gin.H{}
	if in.Restart != "" {
		body["RestartPolicy"] = gin.H{"Name": in.Restart, "MaximumRetryCount": in.Retries}
	}
	if in.MemoryMB != nil || in.CPUs != nil {
		var current struct {
			HostConfig struct{ Memory, MemorySwap, NanoCpus, CpuQuota, CpuPeriod int64 }
		}
		if err := m.json(ctx, "GET", "/containers/"+id+"/json", nil, &current); err != nil {
			fail(c, err)
			return
		}
		host := current.HostConfig
		if in.MemoryMB != nil {
			memory := *in.MemoryMB << 20
			if memory == 0 && host.Memory > 0 {
				response.Fail(c, response.Invalid, "Docker 热更新无法解除现有内存限制，请在原配置中修改")
				return
			}
			if memory > 0 && memory != host.Memory {
				body["Memory"] = memory
				// 保留现有交换空间额度，首次限制内存时使用 Docker 默认比例。
				switch {
				case host.MemorySwap == -1:
					body["MemorySwap"] = int64(-1)
				case host.MemorySwap == 0:
					body["MemorySwap"] = 2 * memory
				case host.MemorySwap < memory:
					body["MemorySwap"] = memory + max(int64(0), host.MemorySwap-host.Memory)
				}
			}
		}
		if in.CPUs != nil {
			cpus := int64(math.Round(*in.CPUs * 1e9))
			if cpus == 0 && (host.NanoCpus > 0 || host.CpuQuota > 0) {
				response.Fail(c, response.Invalid, "Docker 热更新无法解除现有 CPU 限制，请在原配置中修改")
				return
			}
			if cpus > 0 && cpus != host.NanoCpus {
				if host.NanoCpus == 0 && (host.CpuQuota != 0 || host.CpuPeriod > 0) {
					period := host.CpuPeriod
					if period == 0 {
						period = 100000
					}
					body["CpuQuota"] = int64(math.Round(*in.CPUs * float64(period)))
				} else {
					body["NanoCpus"] = cpus
				}
			}
		}
	}
	if len(body) == 0 {
		response.OK(c, gin.H{})
		return
	}
	var out any
	if err := m.json(ctx, "POST", "/containers/"+id+"/update", body, &out); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, out)
}
func (m *Module) removeContainer(c *gin.Context) {
	id, ok := resource(c)
	if !ok {
		return
	}
	release, err := m.tasks.Acquire("docker:container:" + id)
	if err != nil {
		fail(c, err)
		return
	}
	defer release()
	path := "/containers/" + id + "?v=false&force=" + strconv.FormatBool(c.Query("force") == "true")
	if err := m.json(c.Request.Context(), "DELETE", path, nil, nil); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, nil)
}
func (m *Module) remove(c *gin.Context, kind string) {
	id, ok := resource(c)
	if !ok {
		return
	}
	if kind == "networks" {
		if id == "ezpanel-network" {
			response.Fail(c, response.Invalid, "ezpanel-network 为面板内置受管网络，禁止删除")
			return
		}
		var netInfo struct {
			Name string `json:"Name"`
		}
		if err := m.json(c.Request.Context(), "GET", "/networks/"+id, nil, &netInfo); err == nil && netInfo.Name == "ezpanel-network" {
			response.Fail(c, response.Invalid, "ezpanel-network 为面板内置受管网络，禁止删除")
			return
		}
	}
	if err := m.json(c.Request.Context(), "DELETE", "/"+kind+"/"+id, nil, nil); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, nil)
}
func readOutput(r io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, (2<<20)+1))
	if err != nil {
		return "", err
	}
	truncated := len(b) > 2<<20
	if truncated {
		b = b[:2<<20]
	}
	var text strings.Builder
	if len(b) >= 8 && (b[0] == 1 || b[0] == 2) && b[1] == 0 && b[2] == 0 && b[3] == 0 {
		for len(b) >= 8 {
			n := int(binary.BigEndian.Uint32(b[4:8]))
			b = b[8:]
			if n > len(b) {
				text.Write(b)
				break
			}
			text.Write(b[:n])
			b = b[n:]
		}
	} else {
		text.Write(b)
	}
	if truncated {
		text.WriteString("\n[输出已截断，最多显示2MiB]")
	}
	return text.String(), nil
}
func (m *Module) logs(c *gin.Context) {
	id, ok := resource(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	params := url.Values{"stdout": {"true"}, "stderr": {"true"}, "timestamps": {"true"}, "tail": {"500"}}
	for _, key := range []string{"since", "until"} {
		if value := c.Query(key); value != "" {
			if timestamp, err := strconv.ParseInt(value, 10, 64); err != nil || timestamp < 0 {
				response.Fail(c, response.Invalid, "日志时间范围无效")
				return
			}
			params.Set(key, value)
		}
	}
	res, err := m.call(ctx, "GET", "/containers/"+id+"/logs?"+params.Encode(), nil, "")
	if err != nil {
		fail(c, err)
		return
	}
	defer res.Body.Close()
	text, err := readOutput(res.Body)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"content": text})
}
func (m *Module) exec(c *gin.Context) {
	id, ok := resource(c)
	if !ok {
		return
	}
	var in struct {
		Command []string `json:"command"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if len(in.Command) == 0 || len(in.Command) > 64 {
		response.Fail(c, response.Invalid, "请输入命令参数数组")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	defer cancel()
	var out struct {
		ID string `json:"Id"`
	}
	if err := m.json(ctx, "POST", "/containers/"+id+"/exec", gin.H{"AttachStdout": true, "AttachStderr": true, "Cmd": in.Command}, &out); err != nil {
		fail(c, err)
		return
	}
	res, err := m.call(ctx, "POST", "/exec/"+out.ID+"/start", gin.H{"Detach": false, "Tty": false}, "")
	if err != nil {
		fail(c, err)
		return
	}
	defer res.Body.Close()
	text, err := readOutput(res.Body)
	if err != nil {
		fail(c, err)
		return
	}
	var status any
	if err = m.json(ctx, "GET", "/exec/"+out.ID+"/json", nil, &status); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"content": text, "status": status})
}
func (m *Module) search(c *gin.Context) {
	term := strings.TrimSpace(c.Query("term"))
	if term == "" || len(term) > 128 {
		response.Fail(c, response.Invalid, "请输入镜像名称")
		return
	}
	m.read(c, "/images/search?limit=30&term="+url.QueryEscape(term))
}
func (m *Module) imageTransfer(c *gin.Context) {
	var in struct {
		Image      string `json:"image"`
		RegistryID string `json:"registryId"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if !validImage(in.Image) {
		response.Fail(c, response.Invalid, "镜像引用无效")
		return
	}
	authHeader, err := m.registryAuth(in.RegistryID)
	if err != nil {
		fail(c, err)
		return
	}
	action := "pull"
	path := "/images/create?fromImage=" + url.QueryEscape(in.Image)
	if strings.HasSuffix(c.FullPath(), "/push") {
		action = "push"
		path = "/images/" + url.PathEscape(in.Image) + "/push"
	}
	session := auth.Current(c)
	taskID, err := m.tasks.Submit(task.Meta{Module: "docker", Action: action, ResourceID: in.Image, Target: in.Image, LockKey: "docker:image:" + in.Image, Username: session.User.Username, IP: m.auth.ClientIP(c)}, func(ctx context.Context, log *task.Log) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
		defer cancel()
		err := m.stream(ctx, path, authHeader, log.Line)
		return gin.H{"image": in.Image}, err
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": taskID})
}
func (m *Module) tag(c *gin.Context) {
	id, ok := resource(c)
	if !ok {
		return
	}
	var in struct {
		Repository string `json:"repository"`
		Tag        string `json:"tag"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if !validImage(in.Repository) || !resourceName.MatchString(in.Tag) {
		response.Fail(c, response.Invalid, "镜像名或标签无效")
		return
	}
	if err := m.json(c.Request.Context(), "POST", "/images/"+id+"/tag?repo="+url.QueryEscape(in.Repository)+"&tag="+url.QueryEscape(in.Tag), nil, nil); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, nil)
}
func (m *Module) createNetwork(c *gin.Context) {
	var in struct {
		Name     string `json:"name"`
		Driver   string `json:"driver"`
		Subnet   string `json:"subnet"`
		Gateway  string `json:"gateway"`
		Internal bool   `json:"internal"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if !resourceName.MatchString(in.Name) || (in.Driver != "bridge" && in.Driver != "macvlan") {
		response.Fail(c, response.Invalid, "网络名称或驱动无效")
		return
	}
	body := gin.H{"Name": in.Name, "Driver": in.Driver, "Internal": in.Internal, "CheckDuplicate": true}
	if in.Subnet != "" {
		body["IPAM"] = gin.H{"Config": []gin.H{{"Subnet": in.Subnet, "Gateway": in.Gateway}}}
	}
	var out any
	if err := m.json(c.Request.Context(), "POST", "/networks/create", body, &out); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, out)
}
func (m *Module) connectNetwork(c *gin.Context) {
	id, ok := resource(c)
	if !ok {
		return
	}
	var in struct {
		Container  string `json:"container"`
		Disconnect bool   `json:"disconnect"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if !resourceName.MatchString(in.Container) {
		response.Fail(c, response.Invalid, "容器标识无效")
		return
	}
	action := "connect"
	if in.Disconnect {
		action = "disconnect"
	}
	if err := m.json(c.Request.Context(), "POST", "/networks/"+id+"/"+action, gin.H{"Container": in.Container}, nil); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, nil)
}
func (m *Module) createVolume(c *gin.Context) {
	var in struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if !resourceName.MatchString(in.Name) {
		response.Fail(c, response.Invalid, "存储卷名称无效")
		return
	}
	var out any
	if err := m.json(c.Request.Context(), "POST", "/volumes/create", gin.H{"Name": in.Name, "Driver": "local", "Labels": in.Labels}, &out); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, out)
}
func (m *Module) prune(c *gin.Context) {
	kind := c.Param("kind")
	switch kind {
	case "containers", "images", "networks", "volumes":
	default:
		response.Fail(c, response.Invalid, "清理类型无效")
		return
	}
	path := "/" + kind + "/prune"
	if kind == "volumes" {
		path += "?filters=" + url.QueryEscape(`{"all":["true"]}`)
	}
	if kind == "containers" {
		m.pruneStandaloneContainers(c)
		return
	}
	var out any
	if err := m.json(c.Request.Context(), "POST", path, nil, &out); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, out)
}
