package docker

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/netip"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/executor"
	"ezpanel/internal/response"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
)

type containerSpec struct {
	ID, Image, Name    string
	Config, HostConfig map[string]json.RawMessage
	State              struct{ Running, Paused bool }
	Mounts             []struct {
		Type, Name, Source, Destination string
		RW                              bool
	}
	NetworkSettings struct {
		Networks map[string]map[string]json.RawMessage
	}
}

type portMapping struct {
	HostIP    string `json:"hostIp"`
	Host      int    `json:"host"`
	Container int    `json:"container"`
	Protocol  string `json:"protocol"`
}

type containerMountInput struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"readOnly"`
}

func (v containerMountInput) config() map[string]any {
	return map[string]any{"Type": v.Type, "Source": v.Source, "Target": v.Target, "ReadOnly": v.ReadOnly}
}

func containerMounts(input []containerMountInput) ([]map[string]any, error) {
	mounts := make([]map[string]any, 0, len(input))
	targets := make(map[string]bool, len(input))
	for _, v := range input {
		target := path.Clean(v.Target)
		if (v.Type != "bind" && v.Type != "volume") || !strings.HasPrefix(v.Target, "/") ||
			v.Source == "" || (v.Type == "bind" && !strings.HasPrefix(v.Source, "/")) || targets[target] {
			return nil, errors.New("挂载路径无效或容器路径重复")
		}
		mounts = append(mounts, v.config())
		targets[target] = true
	}
	return mounts, nil
}

func containerConfigOwner(labels map[string]string) string {
	switch {
	case labels["ezpanel.managed"] == "true":
		if labels["ezpanel.project"] == "true" {
			return "网站项目设置"
		}
		return "应用商店设置"
	case labels[composeProjectLabel] != "":
		return "编排项目"
	case labels["com.docker.swarm.service.name"] != "":
		return "Swarm 服务"
	default:
		return ""
	}
}

func manageContainerMounts(old containerSpec, input []containerMountInput, requested, mounts []map[string]any, binds []string, host map[string]any) {
	before := map[string]containerMountInput{}
	for _, row := range old.Mounts {
		if row.Type == "bind" || row.Type == "volume" {
			source := row.Source
			if row.Type == "volume" {
				source = row.Name
			}
			before[row.Destination] = containerMountInput{row.Type, source, row.Destination, !row.RW}
		}
	}
	after := map[string]containerMountInput{}
	for _, row := range input {
		after[path.Clean(row.Target)] = row
	}
	keep := func(target string) bool {
		prior, editable := before[target]
		next := after[target]
		return !editable || (prior.Type == next.Type && prior.Source == next.Source)
	}
	readOnlyChanged := func(target string) bool {
		prior, editable := before[target]
		return editable && prior.ReadOnly != after[target].ReadOnly
	}
	preserved := map[string]bool{}
	nextMounts := mounts[:0]
	for _, mount := range mounts {
		target, _ := mount["Target"].(string)
		target = path.Clean(target)
		if keep(target) {
			if readOnlyChanged(target) {
				mount["ReadOnly"] = after[target].ReadOnly
			}
			nextMounts = append(nextMounts, mount)
			preserved[target] = true
		}
	}
	nextBinds := binds[:0]
	for _, bind := range binds {
		parts := strings.SplitN(bind, ":", 3)
		if len(parts) < 2 {
			nextBinds = append(nextBinds, bind)
			continue
		}
		target := path.Clean(parts[1])
		if keep(target) {
			if readOnlyChanged(target) {
				var mode []string
				if len(parts) == 3 {
					mode = strings.Split(parts[2], ",")
					mode = slices.DeleteFunc(mode, func(flag string) bool { return flag == "ro" || flag == "rw" })
				}
				access := "rw"
				if after[target].ReadOnly {
					access = "ro"
				}
				bind = strings.Join(parts[:2], ":") + ":" + strings.Join(append(mode, access), ",")
			}
			nextBinds = append(nextBinds, bind)
			preserved[target] = true
		}
	}
	for i, mount := range requested {
		if !preserved[path.Clean(input[i].Target)] {
			nextMounts = append(nextMounts, mount)
		}
	}
	// 未修改的挂载保留原有传播模式和驱动参数。
	host["Mounts"], host["Binds"] = nextMounts, nextBinds
}

func (m *Module) pruneStandaloneContainers(c *gin.Context) {
	ctx := c.Request.Context()
	var rows []composeContainer
	filters := url.QueryEscape(`{"status":["created","exited","dead"]}`)
	if err := m.json(ctx, "GET", "/containers/json?all=true&filters="+filters, nil, &rows); err != nil {
		fail(c, err)
		return
	}
	deleted, skipped := []string{}, []string{}
	failed := map[string]string{}
	// 逐个清理独立容器，多个 Docker 反向标签过滤会漏掉只命中一类的容器。
	for _, row := range rows {
		switch row.State {
		case "created", "exited", "dead":
		default:
			continue
		}
		if containerConfigOwner(row.Labels) != "" {
			skipped = append(skipped, row.ID)
			continue
		}
		release, err := m.tasks.Acquire("docker:container:" + row.ID)
		if err == nil {
			err = m.json(ctx, "DELETE", "/containers/"+url.PathEscape(row.ID)+"?v=false", nil, nil)
			release()
		}
		if err != nil {
			failed[row.ID] = response.Summary(err.Error())
		} else {
			deleted = append(deleted, row.ID)
		}
	}
	response.OK(c, gin.H{"ContainersDeleted": deleted, "SkippedManaged": skipped, "Errors": failed})
}

func (m *Module) recreateContainer(c *gin.Context) {
	id, ok := resource(c)
	if !ok {
		return
	}
	var in struct {
		Name       string                 `json:"name"`
		Image      string                 `json:"image"`
		Ports      *[]portMapping         `json:"ports"`
		PublishAll bool                   `json:"publishAll"`
		Pull       bool                   `json:"pull"`
		Upgrade    bool                   `json:"upgrade"`
		Force      bool                   `json:"force"`
		RegistryID string                 `json:"registryId"`
		Env        *[]string              `json:"env"`
		Command    *[]string              `json:"command"`
		Mounts     *[]containerMountInput `json:"mounts"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Env != nil {
		for _, entry := range *in.Env {
			name, _, found := strings.Cut(entry, "=")
			if !found || name == "" || strings.ContainsAny(entry, "\x00\r\n") {
				response.Fail(c, response.Invalid, "环境变量须为 NAME=value")
				return
			}
		}
	}
	in.Name, in.Image = strings.TrimSpace(in.Name), strings.TrimSpace(in.Image)
	if (in.Name != "" && !resourceName.MatchString(in.Name)) || (in.Upgrade && in.Image == "") || (in.Image != "" && !validImage(in.Image)) {
		response.Fail(c, response.Invalid, "容器名称或镜像引用无效")
		return
	}
	bindings := map[string][]map[string]string{}
	if in.Ports != nil {
		for _, port := range *in.Ports {
			ip := strings.TrimSpace(port.HostIP)
			_, ipErr := netip.ParseAddr(ip)
			if port.Host < 0 || port.Host > 65535 || port.Container < 1 || port.Container > 65535 ||
				(port.Protocol != "tcp" && port.Protocol != "udp" && port.Protocol != "sctp") || (ip != "" && ipErr != nil) {
				response.Fail(c, response.Invalid, "端口映射或监听地址无效")
				return
			}
			hostPort := ""
			if port.Host != 0 {
				hostPort = strconv.Itoa(port.Host)
			}
			key := strconv.Itoa(port.Container) + "/" + port.Protocol
			bindings[key] = append(bindings[key], map[string]string{"HostIp": ip, "HostPort": hostPort})
		}
	}
	var requestedMounts []map[string]any
	if in.Mounts != nil {
		var err error
		requestedMounts, err = containerMounts(*in.Mounts)
		if err != nil {
			response.Fail(c, response.Invalid, err.Error())
			return
		}
	}
	authHeader, err := m.registryAuth(in.RegistryID)
	if err != nil {
		fail(c, err)
		return
	}
	session := auth.Current(c)
	taskID, err := m.tasks.Submit(task.Meta{Module: "docker", Action: "recreate", ResourceID: id, Target: id, LockKey: "docker:container:" + id, Username: session.User.Username, IP: m.auth.ClientIP(c)}, func(ctx context.Context, log *task.Log) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
		defer cancel()
		var old containerSpec
		containerPath := "/containers/" + id
		if err := m.json(ctx, "GET", containerPath+"/json", nil, &old); err != nil {
			return nil, err
		}
		var labels map[string]string
		_ = json.Unmarshal(old.Config["Labels"], &labels)
		if owner := containerConfigOwner(labels); owner != "" {
			return nil, fmt.Errorf("请从%s修改配置或升级此容器", owner)
		}
		if old.State.Paused || string(old.HostConfig["AutoRemove"]) == "true" {
			return nil, errors.New("请先恢复暂停的容器。自动删除模式的容器无法保留旧容器")
		}
		name := in.Name
		oldName := strings.TrimPrefix(old.Name, "/")
		if name == "" {
			name = oldName
		}
		image := in.Image
		if image == "" {
			image = old.Image
		}
		if in.Image != "" && in.Pull {
			image = imagePullReference(image)
			log.Line("拉取目标镜像 " + image)
			if err := m.stream(ctx, "/images/create?fromImage="+url.QueryEscape(image), authHeader, log.Line); err != nil {
				return nil, err
			}
		}
		var selected struct {
			ID string `json:"Id"`
		}
		if err := m.json(ctx, "GET", "/images/"+url.PathEscape(image)+"/json", nil, &selected); err != nil {
			return nil, err
		}
		if in.Upgrade && !in.Force && selected.ID == old.Image {
			log.Line("容器已使用目标镜像")
			return gin.H{"id": old.ID}, nil
		}
		body := map[string]any{}
		for key, value := range old.Config {
			body[key] = value
		}
		body["Image"] = selected.ID
		if in.Env != nil {
			body["Env"] = *in.Env
		}
		if in.Command != nil {
			body["Cmd"] = *in.Command
		}
		hostConfig := map[string]any{}
		for key, value := range old.HostConfig {
			hostConfig[key] = value
		}
		if in.Ports != nil {
			var exposed map[string]any
			_ = json.Unmarshal(old.Config["ExposedPorts"], &exposed)
			if exposed == nil {
				exposed = map[string]any{}
			}
			for key := range bindings {
				exposed[key] = gin.H{}
			}
			body["ExposedPorts"] = exposed
			hostConfig["PortBindings"] = bindings
			hostConfig["PublishAllPorts"] = in.PublishAll
		}
		// 镜像声明的匿名卷也继续挂载原数据卷。
		var mounts []map[string]any
		var binds, inherited []string
		_ = json.Unmarshal(old.HostConfig["Mounts"], &mounts)
		_ = json.Unmarshal(old.HostConfig["Binds"], &binds)
		_ = json.Unmarshal(old.HostConfig["VolumesFrom"], &inherited)
		volumes := map[string]string{}
		for _, mount := range old.Mounts {
			if mount.Type == "volume" && mount.Name != "" {
				volumes[mount.Destination] = mount.Name
			}
		}
		// Docker 的匿名卷声明不含卷名，重建时须用旧容器实际挂载的卷名。
		for _, mount := range mounts {
			target, _ := mount["Target"].(string)
			if volume := volumes[path.Clean(target)]; volume != "" && mount["Type"] == "volume" && (mount["Source"] == nil || mount["Source"] == "") {
				mount["Source"] = volume
			}
		}
		for i, bind := range binds {
			if volume := volumes[path.Clean(bind)]; volume != "" && !strings.Contains(bind, ":") {
				binds[i] = volume + ":" + bind
				hostConfig["Binds"] = binds
			}
		}
		if in.Mounts != nil {
			if len(inherited) != 0 {
				return nil, errors.New("容器通过 VolumesFrom 继承挂载，请修改原配置")
			}
			manageContainerMounts(old, *in.Mounts, requestedMounts, mounts, binds, hostConfig)
		} else if len(inherited) == 0 {
			targets := map[string]bool{}
			for _, mount := range mounts {
				if target, ok := mount["Target"].(string); ok {
					targets[path.Clean(target)] = true
				}
			}
			for _, bind := range binds {
				parts := strings.Split(bind, ":")
				if len(parts) > 1 {
					targets[path.Clean(parts[1])] = true
				}
			}
			for _, mount := range old.Mounts {
				if mount.Type == "volume" && mount.Name != "" && !targets[mount.Destination] {
					mounts = append(mounts, gin.H{"Type": "volume", "Source": mount.Name, "Target": mount.Destination, "ReadOnly": !mount.RW})
				}
			}
		}
		if in.Mounts == nil && len(mounts) > 0 {
			hostConfig["Mounts"] = mounts
		}
		body["HostConfig"] = hostConfig
		var mode string
		_ = json.Unmarshal(old.HostConfig["NetworkMode"], &mode)
		if mode != "host" && mode != "none" && !strings.HasPrefix(mode, "container:") {
			endpoints := map[string]map[string]json.RawMessage{}
			for network, config := range old.NetworkSettings.Networks {
				endpoint := map[string]json.RawMessage{}
				for _, field := range []string{"IPAMConfig", "Links", "Aliases", "DriverOpts", "GwPriority"} {
					if value := config[field]; value != nil {
						endpoint[field] = value
					}
				}
				endpoints[network] = endpoint
			}
			body["NetworkingConfig"] = gin.H{"EndpointsConfig": endpoints}
		}
		log.Line("保留旧容器与挂载数据，应用新配置")
		// 先禁用旧容器的自动重启，避免备份容器在 Docker 重启后抢占端口。
		policy := old.HostConfig["RestartPolicy"]
		if err := m.json(ctx, "POST", containerPath+"/update", gin.H{"RestartPolicy": gin.H{"Name": "no"}}, nil); err != nil {
			return nil, err
		}
		backup := "ezpanel-backup-" + old.ID[:12] + "-" + strconv.FormatInt(time.Now().Unix(), 10)
		renamed, stopped := false, false
		var out struct {
			ID string `json:"Id"`
		}
		rollback := func(cause error) error {
			recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			var errs []error
			if out.ID != "" {
				errs = append(errs, m.json(recovery, "DELETE", "/containers/"+out.ID+"?force=true&v=false", nil, nil))
			}
			if renamed {
				errs = append(errs, m.json(recovery, "POST", containerPath+"/rename?name="+url.QueryEscape(oldName), nil, nil))
			}
			errs = append(errs, m.json(recovery, "POST", containerPath+"/update", gin.H{"RestartPolicy": policy}, nil))
			if stopped {
				errs = append(errs, m.json(recovery, "POST", containerPath+"/start", nil, nil))
			}
			if err := errors.Join(errs...); err != nil {
				return fmt.Errorf("%w；恢复旧容器失败：%v", cause, err)
			}
			log.Line("已恢复旧容器")
			return cause
		}
		if old.State.Running {
			if err := m.json(ctx, "POST", containerPath+"/stop?t=10", nil, nil); err != nil {
				return nil, rollback(err)
			}
			stopped = true
		}
		if err := m.json(ctx, "POST", containerPath+"/rename?name="+url.QueryEscape(backup), nil, nil); err != nil {
			return nil, rollback(err)
		}
		renamed = true
		if err := m.json(ctx, "POST", "/containers/create?name="+url.QueryEscape(name), body, &out); err != nil {
			return nil, rollback(err)
		}
		if old.State.Running {
			if err := m.json(ctx, "POST", "/containers/"+out.ID+"/start", nil, nil); err != nil {
				return nil, rollback(err)
			}
		}
		log.Line("已创建新容器，旧容器保留为 " + backup)
		return gin.H{"id": out.ID, "name": name, "backup": backup}, nil
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": taskID})
}

func (m *Module) commitContainer(c *gin.Context) {
	id, ok := resource(c)
	if !ok {
		return
	}
	var in struct {
		Image, Author, Comment string
		Export                 bool
	}
	if !response.Bind(c, &in) {
		return
	}
	in.Image = strings.TrimSpace(in.Image)
	if !validImage(in.Image) || strings.Contains(in.Image, "@") || strings.HasPrefix(in.Image, "-") || len(in.Author) > 256 || len(in.Comment) > 1024 {
		response.Fail(c, response.Invalid, "镜像名称、作者或描述无效")
		return
	}
	bin, err := executor.LookPath("docker")
	if err != nil {
		fail(c, err)
		return
	}
	session := auth.Current(c)
	taskID, err := m.tasks.Submit(task.Meta{Module: "docker", Action: "commit", ResourceID: id, Target: in.Image, LockKey: "docker:container:" + id, Username: session.User.Username, IP: m.auth.ClientIP(c)}, func(ctx context.Context, log *task.Log) (any, error) {
		log.Line("生成镜像 " + in.Image + "，挂载数据卷不会进入镜像")
		result, err := executor.RunEnv(ctx, time.Hour, append(executor.DefaultEnv(), "DOCKER_HOST=unix:///var/run/docker.sock"), bin, "container", "commit", "--author", in.Author, "--message", in.Comment, id, in.Image)
		if err != nil {
			return nil, fmt.Errorf("生成镜像失败：%s", result.Stderr)
		}
		imageID := strings.TrimSpace(result.Stdout)
		log.Line("镜像已生成 " + imageID)
		return gin.H{"imageId": imageID, "image": in.Image, "export": in.Export}, nil
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": taskID})
}

func (m *Module) imageArchive(c *gin.Context) {
	id, ok := resource(c)
	if !ok {
		return
	}
	image := c.Query("image")
	path := "/images/" + id + "/get"
	if image != "" {
		if !validImage(image) {
			response.Fail(c, response.Invalid, "镜像引用无效")
			return
		}
		path = "/images/get?names=" + url.QueryEscape(image)
	}
	res, err := m.call(c.Request.Context(), "GET", path, nil, "")
	if err != nil {
		fail(c, err)
		return
	}
	defer res.Body.Close()
	c.Header("Content-Type", "application/gzip")
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "image-" + strings.TrimPrefix(id, "sha256:") + ".tar.gz"}))
	writer := gzip.NewWriter(c.Writer)
	_, _ = io.Copy(writer, res.Body)
	_ = writer.Close()
}
