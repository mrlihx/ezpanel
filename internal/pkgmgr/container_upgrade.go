package pkgmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func newerContainerRelease(id, a, b string) bool {
	if containerBranch(id, a) == "" || containerBranch(id, a) != containerBranch(id, b) {
		return false
	}
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(left), len(right)) {
		var x, y int
		if i < len(left) {
			x, _ = strconv.Atoi(left[i])
		}
		if i < len(right) {
			y, _ = strconv.Atoi(right[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func (m *Manager) upgradeContainerInstance(ctx context.Context, id, instance string, release Release, old containerInfo, log func(string)) (failure error) {
	if id != "openresty" && !IsDatabaseContainer(id) {
		return ErrUnsupported
	}
	if log == nil {
		log = func(string) {}
	}
	current := old.Config.Labels["ezpanel.version"]
	if id == "openresty" && current == "alpine" {
		result, err := m.docker(ctx, 30*time.Second, "run", "--rm", "--pull=never", "--network", "none", old.ImageID, "openresty", "-v")
		if err != nil {
			return err
		}
		current = versionPattern.FindString(result.Stdout + result.Stderr)
	}
	if !newerContainerRelease(id, release.Version, current) {
		return errors.New("请选择当前应用分支中更高的补丁版本")
	}
	if old.State.Status == "restarting" {
		return errors.New("应用正在反复重启，请先修复当前实例再升级")
	}
	// 补丁升级沿用当前端口，待生效的端口由显式启动或重启应用。
	port := old.databasePort(id)
	if IsDatabaseContainer(id) && port == "" {
		return errors.New("无法确认该数据库实例的本机端口")
	}
	platform := releasePlatform(release)
	image, err := m.pullContainer(ctx, release.URL, platform, log)
	if err != nil {
		return err
	}
	binary, versionArg := id, "--version"
	switch id {
	case "openresty":
		versionArg = "-v"
	case "mysql":
		binary = "mysqld"
	case "postgresql":
		binary = "postgres"
	case "mongodb":
		binary = "mongod"
	case "redis":
		binary = "redis-server"
	case "memcached":
		versionArg = "-h"
	}
	checkVersion, err := m.docker(ctx, 45*time.Second, "run", "--rm", "--pull=never", "--platform", platform,
		"--network", "none", "--entrypoint", binary, image, versionArg)
	if err != nil {
		return err
	}
	if version := versionPattern.FindString(checkVersion.Stdout + checkVersion.Stderr); version != release.Version {
		return fmt.Errorf("镜像程序版本为 %q，与所选的 %s 不一致", version, release.Version)
	}
	name := "ezpanel-" + id
	var args []string
	if instance == "default" {
		args, err = m.serviceContainerArgs(id, port)
		if err != nil {
			return err
		}
	} else {
		name += "-" + instance
		var err error
		args, err = m.databaseExtraBindings(id, instance, old.Config.Labels["ezpanel.instance-name"], port)
		if err != nil {
			return err
		}
	}
	previous, candidate := name+"-previous", name+"-candidate"
	command := m.serviceCommand(id)
	if IsDatabaseContainer(id) {
		if err := m.prepareDatabaseConfig(id, instance); err != nil {
			return err
		}
		args = append(args, m.databaseConfigMount(id, instance)...)
		command = m.databaseConfigCommand(id, instance)
	}

	dataDir, dataMount := "", map[string]string{"mysql": "/var/lib/mysql", "postgresql": "/var/lib/postgresql/data", "mongodb": "/data/db", "redis": "/data"}[id]
	if dataMount != "" {
		dataDir = filepath.Join(m.databaseInstanceRoot(id, instance), "data")
		same := false
		for _, mount := range old.Mounts {
			if mount.Destination == dataMount && mount.Source == dataDir {
				same = true
				break
			}
		}
		if !same {
			return errors.New("无法确认该实例的受管数据目录")
		}
	}
	if id == "openresty" {
		check := append([]string{"run", "--rm", "--pull=never", "--platform", platform}, args...)
		if _, err := m.docker(ctx, time.Minute, append(check, image, "openresty", "-t", "-c", m.layout().OpenRestyConfig())...); err != nil {
			return fmt.Errorf("新版 OpenResty 配置检查失败：%w", err)
		}
		log("新版 OpenResty 已通过配置检查")
	}

	if _, exists, err := m.inspectContainer(ctx, previous); err != nil {
		return err
	} else if exists {
		log("清理上次升级留存的旧容器，其数据备份仍在")
		if _, err = m.docker(ctx, 30*time.Second, "rm", "-v", previous); err != nil {
			return err
		}
	}
	if _, exists, err := m.inspectContainer(ctx, candidate); err != nil {
		return err
	} else if exists {
		return errors.New("上次升级的候选容器仍在，请先检查其状态")
	}

	recovery := context.WithoutCancel(ctx)
	stopped, renamed, created, dataSwapped, public := false, false, false, false, false
	var backup string
	defer func() {
		if failure == nil {
			return
		}
		if public && dataDir != "" {
			failure = fmt.Errorf("新版已尝试对外启动，可能已有写入，保留当前数据、旧容器和原数据备份 %s：%w", backup, failure)
			return
		}
		if created {
			if _, err := m.docker(recovery, 30*time.Second, "rm", "-fv", name); err != nil {
				failure = errors.Join(failure, err)
				return
			}
		}
		if dataSwapped {
			if err := os.Rename(dataDir, filepath.Join(filepath.Dir(backup), "failed-data")); err != nil {
				failure = errors.Join(failure, err)
				return
			}
			if err := os.Rename(backup, dataDir); err != nil {
				failure = errors.Join(failure, err)
				return
			}
		}
		if renamed {
			if _, err := m.docker(recovery, 20*time.Second, "rename", old.ID, name); err != nil {
				failure = errors.Join(failure, err)
				return
			}
		}
		if stopped {
			_, err := m.docker(recovery, time.Minute, "start", old.ID)
			failure = errors.Join(failure, err)
		}
	}()
	if old.State.Running {
		if id == "redis" {
			result, err := m.docker(ctx, 10*time.Second, "exec", old.ID, "sh", "-c",
				`REDISCLI_AUTH="$(cat /run/ezpanel-password)" exec redis-cli --raw CONFIG GET appendonly save`)
			if err != nil {
				return err
			}
			lines := strings.Split(result.Stdout, "\n")
			persistent := false
			for i := 0; i+1 < len(lines); i += 2 {
				if lines[i] == "appendonly" && lines[i+1] == "yes" || lines[i] == "save" && strings.TrimSpace(lines[i+1]) != "" {
					persistent = true
					break
				}
			}
			if !persistent {
				return errors.New("无法确认 Redis 已启用持久化，请先开启 AOF 或 RDB 再升级")
			}
		}
		log("进入切换窗口，正常停止旧服务")
		stop := []string{"stop", "--time", "90"}
		if id == "openresty" {
			stop = append(stop, "--signal", "SIGQUIT")
		}
		if _, err := m.docker(ctx, 2*time.Minute, append(stop, old.ID)...); err != nil {
			return err
		}
		stopped = true
	}
	if dataDir != "" {
		info, _, err := m.inspectContainer(ctx, name)
		if err != nil {
			return err
		}
		if info.State.Running || (old.State.Running && info.State.ExitCode != 0) {
			return errors.New("旧数据库未正常退出，取消数据切换")
		}
		backupDir, err := os.MkdirTemp(filepath.Dir(dataDir), "."+filepath.Base(dataDir)+"-upgrade-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(filepath.Join(backupDir, "staged"))
		stage := filepath.Join(backupDir, "staged")
		backup = filepath.Join(backupDir, "original")
		// 只允许隔离容器写入副本，旧版数据直到切换完成都保持原样。
		log("正在创建隔离数据副本")
		cp, err := m.LookPath("cp")
		if err != nil {
			return err
		}
		if _, err = m.Stream(ctx, 30*time.Minute, nil, log, cp, "-a", "--reflink=auto", dataDir, stage); err != nil {
			return err
		}
		if path := m.databaseConfigPath(id, instance); path != "" {
			if _, err := os.Stat(path); err == nil {
				if _, err = m.Run(ctx, 10*time.Second, nil, cp, "-p", path, filepath.Join(backupDir, filepath.Base(path))); err != nil {
					return err
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		private := []string{"--network", "none"}
		mysqlSocketDir := filepath.Dir(m.MySQLSocket())
		for i := 0; i < len(args); i += 2 {
			key, value := args[i], args[i+1]
			if key == "-p" {
				continue
			}
			if key == "--mount" {
				value = strings.Replace(value, "src="+dataDir+",dst="+dataMount, "src="+stage+",dst="+dataMount, 1)
				if id == "mysql" && instance == "default" && strings.Contains(value, ",dst="+mysqlSocketDir) {
					isolatedRun := filepath.Join(backupDir, "run")
					if err = os.Mkdir(isolatedRun, 0700); err != nil {
						return err
					}
					defer os.RemoveAll(isolatedRun)
					if err = os.Chmod(isolatedRun, 0777); err != nil {
						return err
					}
					value = "type=bind,src=" + isolatedRun + ",dst=" + mysqlSocketDir
				}
			}
			private = append(private, key, value)
		}
		if err := m.createServiceContainer(ctx, []string{"run", "-d"}, candidate, release.Version, platform, private, image, command); err != nil {
			_, _ = m.docker(recovery, 30*time.Second, "rm", "-fv", candidate)
			return err
		}
		ready := m.waitDatabaseAfterConfig(ctx, id, instance, candidate)
		_, halt := m.docker(recovery, 2*time.Minute, "stop", "--time", "90", candidate)
		stagedInfo, _, inspectErr := m.inspectContainer(recovery, candidate)
		_, remove := m.docker(recovery, 30*time.Second, "rm", "-fv", candidate)
		if err := errors.Join(ready, halt, inspectErr, remove); err != nil {
			return err
		}
		if stagedInfo.State.ExitCode != 0 {
			return errors.New("隔离数据库未正常退出，取消数据切换")
		}
		if err = os.Rename(dataDir, backup); err != nil {
			return err
		}
		if err = os.Rename(stage, dataDir); err != nil {
			if restore := os.Rename(backup, dataDir); restore != nil {
				stopped = false
				return errors.Join(err, restore)
			}
			return err
		}
		dataSwapped = true
		log("新版数据库已在隔离环境通过验证，原数据备份：" + backup)
	}
	if _, err = m.docker(ctx, 20*time.Second, "rename", old.ID, previous); err != nil {
		return err
	}
	renamed = true
	if err = m.createServiceContainer(ctx, []string{"create"}, name, release.Version, platform, args, image, command); err != nil {
		return err
	}
	created = true
	if old.State.Running {
		public = true
		if _, err = m.docker(ctx, time.Minute, "start", name); err != nil {
			return err
		}
		if IsDatabaseContainer(id) {
			if err = m.waitDatabaseAfterConfig(ctx, id, instance, name); err != nil {
				return err
			}
		} else {
			if _, err = m.docker(ctx, 30*time.Second, "exec", name, "openresty", "-t", "-c", m.layout().OpenRestyConfig()); err != nil {
				return err
			}
		}
	}
	log("新版已接管，保留旧容器和已有配置")
	return nil
}
