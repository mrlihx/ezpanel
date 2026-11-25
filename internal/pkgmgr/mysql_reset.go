package pkgmgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// 用一次性init-file恢复本机root，不依赖旧密码，不开启免认证监听。
func (m *Manager) ResetMySQLRoot(ctx context.Context, password string, verify func(context.Context) error, log func(string)) (err error) {
	if err = m.lock("mysql"); err != nil {
		return err
	}
	defer m.unlock("mysql")
	state, err := m.Status(ctx, "mysql")
	if err != nil {
		return err
	}
	if !state.Installed || !state.Manageable || m.upgradeBusy(ctx, "mysql") {
		return errors.New("当前MySQL无法执行受控恢复，或正在升级")
	}
	if UsesDocker("mysql") && state.PkgManager == "Docker" {
		return m.resetDockerMySQLRoot(ctx, state, password, verify, log)
	}
	if state.Unit == "" {
		return errors.New("当前MySQL无法执行受控恢复，或正在升级")
	}
	settings, err := m.Settings(ctx, "mysql")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(settings.Content, "\n") {
		key, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && (strings.TrimSpace(key) == "init-file" || strings.TrimSpace(key) == "init_file") {
			return errors.New("MySQL已有初始化文件配置，请先处理后再重置")
		}
	}
	dataDir := state.DataDir
	if dataDir == "" {
		dataDir = "/var/lib/mysql"
		for _, line := range strings.Split(settings.Content, "\n") {
			key, value, ok := strings.Cut(line, "=")
			if ok && strings.TrimSpace(key) == "datadir" {
				dataDir = strings.Trim(strings.TrimSpace(value), `"'`)
			}
		}
	}
	dataInfo, err := os.Stat(dataDir)
	if err != nil || !dataInfo.IsDir() {
		return errors.New("无法确认MySQL数据目录，未修改服务")
	}
	configInfo, err := os.Stat(settings.Path)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dataDir, ".panel-root-reset-*.sql")
	if err != nil {
		return err
	}
	initPath := file.Name()
	defer os.Remove(initPath)
	// 显式SQL模式配合单引号翻倍，不把密码放进命令行或任务日志。
	sql := "SET SESSION sql_mode = 'NO_BACKSLASH_ESCAPES';\nALTER USER 'root'@'localhost' IDENTIFIED BY '" + strings.ReplaceAll(password, "'", "''") + "';\n"
	if err = file.Chmod(0600); err == nil {
		if owner, ok := dataInfo.Sys().(*syscall.Stat_t); ok {
			err = file.Chown(int(owner.Uid), int(owner.Gid))
		}
	}
	if err == nil {
		_, err = file.WriteString(sql)
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	configPath, err := filepath.EvalSymlinks(settings.Path)
	if err != nil {
		return err
	}
	writeConfig := func(content string) error {
		temp, err := os.CreateTemp(filepath.Dir(configPath), ".panel-root-config-")
		if err != nil {
			return err
		}
		defer os.Remove(temp.Name())
		if err = temp.Chmod(configInfo.Mode().Perm()); err == nil {
			if owner, ok := configInfo.Sys().(*syscall.Stat_t); ok {
				err = temp.Chown(int(owner.Uid), int(owner.Gid))
			}
		}
		if err == nil {
			_, err = temp.WriteString(content)
		}
		closeErr := temp.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		return os.Rename(temp.Name(), configPath)
	}
	modified := false
	cleanup := func() error {
		if !modified {
			return nil
		}
		if e := writeConfig(settings.Content); e != nil {
			return e
		}
		modified = false
		return os.Remove(initPath)
	}
	defer func() {
		if modified {
			err = errors.Join(err, cleanup())
		}
	}()
	log("正在停止本机MySQL并应用一次性ROOT恢复配置")
	if _, err = m.Run(ctx, 2*time.Minute, nil, "/usr/bin/systemctl", "stop", state.Unit); err != nil {
		return errors.New("停止MySQL失败，未修改密码")
	}
	changed := settings.Content + "\n[mysqld]\ninit-file=" + filepath.Clean(initPath) + "\n"
	if err = writeConfig(changed); err != nil {
		_, _ = m.Run(context.WithoutCancel(ctx), 2*time.Minute, nil, "/usr/bin/systemctl", "start", state.Unit)
		return err
	}
	modified = true
	_, startErr := m.Run(ctx, 10*time.Minute, nil, "/usr/bin/systemctl", "start", state.Unit)
	if startErr == nil {
		startErr = verify(ctx)
	}
	restoreErr := cleanup()
	if restoreErr != nil {
		return errors.Join(errors.New("恢复原MySQL配置失败，请检查服务配置"), restoreErr)
	}
	// 恢复启动参数后再启动，避免下一次启动仍依赖包含密码的临时文件。
	_, restartErr := m.Run(context.WithoutCancel(ctx), 10*time.Minute, nil, "/usr/bin/systemctl", "restart", state.Unit)
	if startErr != nil {
		return errors.Join(errors.New("ROOT恢复未确认成功，已恢复原启动配置；请检查数据库自身密码策略和日志"), startErr, restartErr)
	}
	if restartErr != nil {
		return errors.New("ROOT密码已修改，但正常模式重启失败，请检查MySQL服务")
	}
	log("ROOT恢复完成，已清理临时文件并恢复正常启动配置")
	return nil
}

func (m *Manager) resetDockerMySQLRoot(ctx context.Context, state State, password string, verify func(context.Context) error, log func(string)) (err error) {
	if log == nil {
		log = func(string) {}
	}
	const (
		containerName  = "ezpanel-mysql"
		resetContainer = "ezpanel-mysql-root-reset"
	)
	info, exists, err := m.inspectContainer(ctx, containerName)
	if err != nil {
		return err
	}
	if !exists || info.ImageID == "" {
		return errors.New("未找到受管的 MySQL 容器，无法重置密码")
	}
	dataDir := state.DataDir
	if dataDir == "" {
		dataDir = m.serviceDataDir("mysql")
	}
	dataInfo, err := os.Stat(dataDir)
	if err != nil || !dataInfo.IsDir() {
		return errors.New("无法确认MySQL数据目录，未修改服务")
	}
	file, err := os.CreateTemp(dataDir, ".panel-root-reset-*.sql")
	if err != nil {
		return err
	}
	initPath := file.Name()
	defer os.Remove(initPath)
	escaped := strings.ReplaceAll(password, "'", "''")
	// 容器版 MySQL 同时维护 localhost 和 % 两个 root 账号，确保宿主机端口转发与 socket 连接均生效。
	sql := "SET SESSION sql_mode = 'NO_BACKSLASH_ESCAPES';\n" +
		"ALTER USER IF EXISTS 'root'@'localhost' IDENTIFIED BY '" + escaped + "';\n" +
		"ALTER USER IF EXISTS 'root'@'%' IDENTIFIED BY '" + escaped + "';\n" +
		"FLUSH PRIVILEGES;\n"
	if err = file.Chmod(0600); err == nil {
		if owner, ok := dataInfo.Sys().(*syscall.Stat_t); ok {
			err = file.Chown(int(owner.Uid), int(owner.Gid))
		}
	}
	if err == nil {
		_, err = file.WriteString(sql)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	recovery := context.WithoutCancel(ctx)
	log("正在停止 MySQL 容器并准备隔离重置环境")
	if _, err = m.docker(ctx, 2*time.Minute, "stop", "--time", "60", containerName); err != nil {
		return errors.New("停止 MySQL 容器失败，未修改密码")
	}
	_, _ = m.docker(recovery, 20*time.Second, "rm", "-f", resetContainer)

	containerInitFile := "/var/lib/mysql/" + filepath.Base(initPath)
	log("正在使用断网隔离容器执行一次性 ROOT 密码重置脚本")
	_, resetErr := m.docker(ctx, 2*time.Minute,
		"run", "-d", "--name", resetContainer, "--network", "none",
		"--mount", "type=bind,src="+dataDir+",dst=/var/lib/mysql",
		info.ImageID,
		"mysqld",
		"--init-file="+containerInitFile,
		"--skip-networking",
		"--mysqlx=OFF",
		"--event-scheduler=OFF",
		"--skip-replica-start",
	)
	if resetErr == nil {
		deadline := time.Now().Add(45 * time.Second)
		readyErr := errors.New("隔离重置容器未在超时时间内就绪")
		for time.Now().Before(deadline) {
			if _, pingErr := m.docker(ctx, 10*time.Second, "exec", "-e", "MYSQL_PWD="+password, resetContainer, "mysqladmin", "--protocol=socket", "-uroot", "ping"); pingErr == nil {
				readyErr = nil
				break
			} else {
				readyErr = pingErr
			}
			select {
			case <-ctx.Done():
				readyErr = ctx.Err()
				deadline = time.Time{}
			case <-time.After(1500 * time.Millisecond):
			}
		}
		resetErr = readyErr
		_, stopErr := m.docker(recovery, time.Minute, "stop", "--time", "30", resetContainer)
		_, rmErr := m.docker(recovery, 20*time.Second, "rm", "-f", resetContainer)
		if resetErr == nil {
			resetErr = errors.Join(stopErr, rmErr)
		}
	} else {
		_, _ = m.docker(recovery, 20*time.Second, "rm", "-f", resetContainer)
	}
	_ = os.Remove(initPath)

	log("正在恢复启动 MySQL 正式容器并校验新密码")
	_, startErr := m.docker(recovery, 2*time.Minute, "start", containerName)
	if resetErr != nil {
		return errors.Join(errors.New("ROOT 密码重置失败，已恢复启动原 MySQL 容器；请检查密码策略与容器日志"), resetErr, startErr)
	}
	if startErr != nil {
		return errors.Join(errors.New("ROOT 密码已重置，但启动 MySQL 容器失败，请检查容器状态"), startErr)
	}
	var verifyErr error
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if err := verify(ctx); err == nil {
			verifyErr = nil
			break
		} else {
			verifyErr = err
		}
		select {
		case <-ctx.Done():
			verifyErr = ctx.Err()
			deadline = time.Time{}
		case <-time.After(1500 * time.Millisecond):
		}
	}
	if verifyErr != nil {
		return errors.Join(errors.New("MySQL 容器已启动，但新密码连接校验失败"), verifyErr)
	}
	if state.InitialPasswordPath != "" {
		_ = os.WriteFile(state.InitialPasswordPath, []byte(password), 0600)
	}
	log("MySQL 容器 ROOT 密码恢复完成，已清理临时文件并恢复正常服务")
	return nil
}
