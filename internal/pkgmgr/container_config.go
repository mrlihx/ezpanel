package pkgmgr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"ezpanel/internal/config"
	"fmt"
	"os"
	"path/filepath"
)

const mysqlErrorLog = "--log-error=" + containerMySQLErrorLog
const mysqlAuthLogging = "--log-error-verbosity=3"

func (m *Manager) MySQLSocket() string {
	return m.layout().MySQLSocket("default")
}

func (m *Manager) PHPSocket(branch string) string {
	return m.phpActiveSocket(branch, "fpm.sock")
}

func PHPSocket(layout config.Layout, branch string) string {
	return phpActiveSocket(layout, branch, "fpm.sock")
}

func (m *Manager) PHPMyAdminData() string {
	return m.layout().PHPMyAdminPrivateDir()
}

func (m *Manager) pmaSocket(branch string) string {
	return m.phpActiveSocket(branch, "phpmyadmin.sock")
}

func (m *Manager) serviceContainerArgs(id, port string) ([]string, error) {
	l := m.layout()
	if err := m.ensureAppParents(id); err != nil {
		return nil, err
	}
	root := m.defaultInstallRoot(id)
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	if err := os.Chmod(root, 0755); err != nil {
		return nil, err
	}
	if port == "" && IsDatabaseContainer(id) {
		port = m.databaseHostPort(id, "default", databasePorts[id])
	}
	mount := func(path, dest string, readonly bool) string {
		option := "type=bind,src=" + path + ",dst=" + dest
		if readonly {
			option += ",readonly"
		}
		return option
	}
	switch id {
	case "openresty":
		certs := filepath.Join(l.DataDir(), "certs")
		siteRoot := m.cfg.Current().SiteRoot
		for _, path := range []string{siteRoot, l.PHPBaseDir()} {
			if err := EnsureWebDirectory(path); err != nil {
				return nil, err
			}
		}
		openRestyLogDir, err := m.ensureContainerLogDir("openresty", "")
		if err != nil {
			return nil, err
		}
		wafLogDir, err := m.ensureContainerLogDir("waf", "")
		if err != nil {
			return nil, err
		}
		if err := ensurePublicTree(l.OpenRestyConfDir(), l.ACMEDir(), WebsiteLogRoot); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(certs, 0700); err != nil {
			return nil, err
		}
		if _, err := os.Stat(l.OpenRestyConfig()); errors.Is(err, os.ErrNotExist) {
			cfgText := "worker_processes auto;\npid /tmp/ezpanel-openresty.pid;\nerror_log " + containerOpenRestyLog + " notice;\nevents { worker_connections 1024; }\nhttp {\n    include /usr/local/openresty/nginx/conf/mime.types;\n    include " + l.OpenRestyConfDir() + "/*.conf;\n}\n"
			if err = os.WriteFile(l.OpenRestyConfig(), []byte(cfgText), 0644); err != nil {
				return nil, err
			}
		}
		return []string{
			"--network", "host",
			"--mount", mount(root, root, true),
			"--mount", mount(l.PHPBaseDir(), l.PHPBaseDir(), true),
			"--mount", mount(l.ACMEDir(), l.ACMEDir(), true),
			"--mount", mount(siteRoot, siteRoot, true),
			"--mount", mount(certs, certs, true),
			"--mount", mount(WebsiteLogRoot, WebsiteLogRoot, false),
			"--mount", mount(openRestyLogDir, ContainerLogDir, false),
			"--mount", mount(wafLogDir, containerWAFLogDir, false),
		}, nil
	case "redis", "mysql", "postgresql", "mongodb":
		data, state := m.serviceDataDir(id), m.serviceStateDir(id)
		for _, path := range []string{data, state} {
			if err := os.MkdirAll(path, 0750); err != nil {
				return nil, err
			}
		}
		logDir, err := m.ensureContainerLogDir(id, "default")
		if err != nil {
			return nil, err
		}
		secret := filepath.Join(state, "root-password")
		if _, err := os.Stat(secret); errors.Is(err, os.ErrNotExist) {
			var random [18]byte
			if _, err = rand.Read(random[:]); err != nil {
				return nil, err
			}
			if err = os.WriteFile(secret, []byte(hex.EncodeToString(random[:])), 0600); err != nil {
				return nil, err
			}
		}
		args := []string{
			"--mount", mount(secret, "/run/ezpanel-password", true),
			"--mount", mount(logDir, ContainerLogDir, false),
		}
		bindIP := m.databaseBindIP(id, "default")
		switch id {
		case "mysql":
			socketDir := filepath.Dir(m.MySQLSocket())
			if err := os.MkdirAll(socketDir, 0710); err != nil {
				return nil, err
			}
			if err := m.allowPHPMyAdminMySQLSocket(); err != nil {
				return nil, err
			}
			return append(args, "-p", bindIP+":"+port+":3306", "--mount", mount(data, "/var/lib/mysql", false), "--mount", mount(socketDir, socketDir, false), "-e", "MYSQL_ROOT_PASSWORD_FILE=/run/ezpanel-password"), nil
		case "postgresql":
			return append(args, "-p", bindIP+":"+port+":5432", "--mount", mount(data, "/var/lib/postgresql/data", false), "-e", "PGDATA=/var/lib/postgresql/data", "-e", "POSTGRES_PASSWORD_FILE=/run/ezpanel-password"), nil
		case "mongodb":
			password, err := os.ReadFile(secret)
			if err != nil {
				return nil, err
			}
			env := filepath.Join(state, "mongo.env")
			if err = os.WriteFile(env, []byte("MONGO_INITDB_ROOT_USERNAME=panelAdmin\nMONGO_INITDB_ROOT_PASSWORD="+string(password)+"\n"), 0600); err != nil {
				return nil, err
			}
			return []string{"-p", bindIP + ":" + port + ":27017", "--mount", mount(data, "/data/db", false), "--mount", mount(logDir, ContainerLogDir, false), "--env-file", env}, nil
		default:
			return append(args, "-p", bindIP+":"+port+":6379", "--mount", mount(data, "/data", false)), nil
		}
	case "memcached":
		logDir, err := m.ensureContainerLogDir("memcached", "default")
		if err != nil {
			return nil, err
		}
		return []string{"-p", m.databaseBindIP("memcached", "default") + ":" + port + ":11211", "--mount", mount(logDir, ContainerLogDir, false)}, nil
	default:
		siteRoot := m.cfg.Current().SiteRoot
		appRoot := filepath.Dir(m.PHPMyAdminRoot())
		pmaData := m.PHPMyAdminData()
		phpDir := m.phpDirectory(id)
		socketDir := filepath.Join(root, "run")
		logDir, err := m.ensureContainerLogDir(id, "")
		if err != nil {
			return nil, err
		}
		for _, dir := range []string{siteRoot, l.PHPBaseDir(), socketDir} {
			if err := EnsureWebDirectory(dir); err != nil {
				return nil, err
			}
		}
		if err := ensurePublicTree(appRoot, phpDir); err != nil {
			return nil, err
		}
		for _, dir := range []string{filepath.Dir(m.MySQLSocket()), pmaData, pmaData + "/sessions", pmaData + "/tmp"} {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, err
			}
		}
		for _, dir := range []string{pmaData, pmaData + "/sessions", pmaData + "/tmp"} {
			if err := os.Chown(dir, 65534, phpMyAdminGroup); err != nil {
				return nil, err
			}
			if err := os.Chmod(dir, 0700); err != nil {
				return nil, err
			}
		}
		fpm := fmt.Sprintf(`[global]
daemonize = no
error_log = %s

[www]
user = www-data
group = www-data
listen = %s
listen.mode = 0666
pm = ondemand
pm.max_children = 10
pm.process_idle_timeout = 10s
pm.max_requests = 500
catch_workers_output = yes
security.limit_extensions = .php
pm.status_path = /__ezpanel_fpm_status

[phpmyadmin]
user = nobody
group = nogroup
listen = %s
listen.mode = 0600
pm = ondemand
pm.max_children = 5
request_terminate_timeout = 120s
security.limit_extensions = .php
php_admin_flag[display_errors] = off
php_admin_value[open_basedir] = %s:%s:/etc/ssl/certs:%s
php_admin_value[session.save_path] = %s/sessions
php_admin_flag[session.cookie_httponly] = on
php_admin_value[session.cookie_samesite] = Strict
php_admin_value[upload_tmp_dir] = %s/tmp
php_admin_value[upload_max_filesize] = 64M
php_admin_value[post_max_size] = 64M
`, containerPHPFPMErrorLog, filepath.Join(socketDir, "fpm.sock"), filepath.Join(socketDir, "phpmyadmin.sock"), appRoot, pmaData, filepath.Dir(m.MySQLSocket()), pmaData, pmaData)
		defaultPHPIni := "upload_max_filesize = 50M\npost_max_size = 50M\nmax_execution_time = 100\ndisable_functions = passthru,exec,system,putenv,chroot,chgrp,chown,shell_exec,popen,proc_open,pcntl_exec,ini_alter,ini_restore,dl,openlog,syslog,readlink,symlink,popepassthru,pcntl_alarm,pcntl_fork,pcntl_waitpid,pcntl_wait,pcntl_wifexited,pcntl_wifstopped,pcntl_wifsignaled,pcntl_wifcontinued,pcntl_wexitstatus,pcntl_wtermsig,pcntl_wstopsig,pcntl_signal,pcntl_signal_dispatch,pcntl_get_last_error,pcntl_strerror,pcntl_sigprocmask,pcntl_sigwaitinfo,pcntl_sigtimedwait,pcntl_getpriority,pcntl_setpriority,imap_open,apache_setenv\n"
		for name, content := range map[string]string{"php-fpm.conf": fpm, "php.ini": defaultPHPIni} {
			path := filepath.Join(phpDir, name)
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				if err = os.WriteFile(path, []byte(content), 0644); err != nil {
					return nil, err
				}
			}
		}
		return []string{
			"--mount", mount(phpDir, phpDir, true),
			"--mount", mount(socketDir, socketDir, false),
			"--mount", mount(logDir, ContainerLogDir, false),
			"--mount", mount(siteRoot, siteRoot, false),
			"--mount", mount(appRoot, appRoot, true),
			"--mount", mount(pmaData, pmaData, false),
			"--mount", mount(filepath.Dir(m.MySQLSocket()), filepath.Dir(m.MySQLSocket()), true),
		}, nil
	}
}

func (m *Manager) serviceCommand(id string) []string {
	l := m.layout()
	switch id {
	case "openresty":
		return []string{"openresty", "-g", "daemon off;", "-c", l.OpenRestyConfig()}
	case "mysql":
		return []string{"mysqld", "--socket=" + m.MySQLSocket(), mysqlErrorLog, mysqlAuthLogging, "--skip-name-resolve"}
	case "redis":
		return []string{"sh", "-c", `exec docker-entrypoint.sh redis-server --appendonly yes --requirepass "$(cat /run/ezpanel-password)"`}
	case "memcached":
		return []string{"memcached", "-U", "0"}
	}
	if IsPHP(id) {
		return []string{"php-fpm", "-c", m.phpDirectory(id) + "/php.ini", "-y", m.phpDirectory(id) + "/php-fpm.conf"}
	}
	return nil
}

func (m *Manager) uninstallDocker(ctx context.Context, id string) (State, error) {
	if IsProjectRuntime(id) {
		return State{}, errors.New("请选择要卸载的运行环境")
	}
	return m.dockerService(ctx, id, "remove")
}
