package main

import (
	"context"
	"crypto/rand"
	"errors"
	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/geoip"
	"ezpanel/internal/logview"
	"ezpanel/internal/module/alerts"
	backupmodule "ezpanel/internal/module/backup"
	certificatemodule "ezpanel/internal/module/certificate"
	cronmodule "ezpanel/internal/module/cron"
	"ezpanel/internal/module/database"
	dockermodule "ezpanel/internal/module/docker"
	filemodule "ezpanel/internal/module/file"
	"ezpanel/internal/module/oplog"
	"ezpanel/internal/module/phpmyadmin"
	securitymodule "ezpanel/internal/module/security"
	settingsmodule "ezpanel/internal/module/settings"
	"ezpanel/internal/module/software"
	"ezpanel/internal/module/sysinfo"
	terminalmodule "ezpanel/internal/module/terminal"
	"ezpanel/internal/module/toolbox"
	wafmodule "ezpanel/internal/module/waf"
	"ezpanel/internal/module/website"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/plugin"
	"ezpanel/internal/server"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"ezpanel/internal/version"
	"flag"
	"fmt"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"syscall"
	"time"
)

func main() {
	args := os.Args[1:]
	var err error
	if len(args) == 0 {
		err = runMenu()
	} else {
		err = run(args)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		printHelp()
		return nil
	}
	command := "server"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		command, args = args[0], args[1:]
	}
	showHelp := command == "help"
	if showHelp {
		if len(args) != 1 {
			return errors.New("用法：ezpanel help [命令]")
		}
		if args[0] != "--help" && args[0] != "-h" {
			command = args[0]
		}
	}
	index := slices.IndexFunc(panelCommands, func(item panelCommand) bool { return item.name == command })
	if index < 0 {
		return fmt.Errorf("未知命令 %q，请运行 ezpanel help", command)
	}
	if showHelp {
		panelCommands[index].printHelp()
		return nil
	}
	flags := flag.NewFlagSet("ezpanel "+command, flag.ContinueOnError)
	flags.Usage = panelCommands[index].printHelp
	install := flags.String("install-dir", "/opt/ezpanel", "安装目录")
	dev := flags.Bool("dev", false, "开发模式，保留认证与来源校验")
	init := flags.Bool("init", false, "交互初始化后退出")
	releaseTag := false
	options := adminOptions{}
	switch command {
	case "version":
		flags.BoolVar(&releaseTag, "tag", false, "显示完整发布标签")
	case "reset-port":
		flags.IntVar(&options.port, "port", 0, "新监听端口")
	case "external-access":
		flags.StringVar(&options.accessMode, "mode", "", "on 开启或 off 关闭")
	case "reset-entry":
		flags.StringVar(&options.entry, "entry", "", "新安全入口，留空随机生成")
	case "reset-username":
		flags.StringVar(&options.username, "username", "", "新管理员用户名")
	case "unlock-login":
		flags.StringVar(&options.ip, "ip", "", "解锁的来源 IP")
	case "logs":
		flags.BoolVar(&options.follow, "follow", false, "持续显示日志")
	}
	if e := flags.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("存在未识别的参数，请运行 ezpanel help")
	}
	if command == "version" {
		if releaseTag {
			fmt.Println(version.ReleaseTag)
		} else {
			fmt.Println(version.Version)
		}
		return nil
	}
	if !*dev && (runtime.GOOS != "linux" || os.Geteuid() != 0) {
		return errors.New("正式模式仅支持Linux本机root")
	}
	syscall.Umask(0077)
	if *init {
		if command != "server" {
			return errors.New("仅server支持初始化")
		}
		return initialize(*install, *dev)
	}
	switch command {
	case "start", "stop", "restart", "status", "logs":
		return panelServiceCommand(command, options.follow)
	}
	if command == "hardening-recover" {
		return recoverHardening(*install)
	}
	cfg, e := config.Load(*install)
	if e != nil {
		return errors.New("未初始化或配置无效，请在本机交互运行 ezpanel server --init")
	}
	cfg.Dev = *dev
	if command == "ready" {
		return readyCheck(cfg)
	}
	if command == "upgrade-check" {
		return upgradeCheck(cfg)
	}
	addr := net.JoinHostPort(cfg.ListenAddr, strconv.Itoa(cfg.Port))
	switch command {
	case "info":
		return printPanelInfo(cfg)
	case "reset-port", "reset-entry", "external-access":
		return resetPanelAccess(command, cfg, options)
	case "reset-username", "reset-password", "unlock-login":
		return resetPanelAccount(command, cfg, options)
	}
	db, e := store.Open(filepath.Join(cfg.DataDir(), "panel.db"))
	if e != nil {
		return e
	}
	store.SetOpLogPath(db, filepath.Join(cfg.Layout().LogDir(), "panel", "op_logs.jsonl"))
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	var count int64
	if db.Model(&store.User{}).Count(&count).Error != nil || count == 0 {
		return errors.New("管理员未初始化，拒绝启动")
	}
	runtimeLog, e := logview.StartRuntime(cfg.DataDir())
	if e != nil {
		return e
	}
	defer runtimeLog.Close()
	cfg = config.Shared(cfg)
	a, e := auth.New(db, cfg)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer geoip.Open(ctx, filepath.Join(cfg.DataDir(), "geoip"))()
	tasks, e := task.New(ctx, db, cfg)
	if e != nil {
		return e
	}
	defer tasks.Close()
	gin.SetMode(gin.ReleaseMode)
	router, e := server.New(server.Options{Auth: a, Tasks: tasks})
	if e != nil {
		return e
	}
	metrics := sysinfo.New(ctx, a)
	defer metrics.Close()
	router.RegisterModule(metrics)
	files, e := filemodule.New(db, cfg, tasks, a)
	if e != nil {
		return e
	}
	router.RegisterModule(files)
	terminals := terminalmodule.New(ctx, db, a, cfg.AESKey)
	defer terminals.Close()
	router.RegisterModule(terminals)
	packages := pkgmgr.New()
	packages.Configure(cfg)
	packages.EnsureDefaultDockerMirrors(ctx)
	packages.EnsureManagedHosts()
	go func() { _, _ = packages.PHPCLIStatus(ctx) }()
	go packages.Cleanup(ctx)
	go packages.StartDailyUpdateCheck(ctx)
	security := securitymodule.New(cfg, a)
	if e = security.ConfigureHardening(ctx); e != nil {
		return e
	}
	websites, e := website.New(db, cfg, tasks, packages, a, security)
	if e != nil {
		return e
	}
	router.RegisterModule(websites)
	certificates := certificatemodule.New(db, cfg, tasks, a, websites.PrepareACME)
	router.RegisterModule(certificates)
	applications := software.New(db, cfg, tasks, packages, a, websites.CheckEnvironment)
	packages.SetDatabasePortSync(security.SyncDatabasePort)
	router.RegisterModule(applications)
	go applications.RunClamSchedule(ctx)
	go applications.RunRsyncSchedule(ctx)
	databases := database.New(db, cfg, tasks, packages, a)
	metrics.SetSummarySources(db, databases.CountDatabases)
	router.RegisterModule(databases)
	router.RegisterModule(phpmyadmin.New(db, cfg, packages, a))
	docker := dockermodule.New(db, cfg, tasks, packages, a)
	router.RegisterModule(docker)
	backups := backupmodule.New(db, cfg, tasks, a, nil)
	router.RegisterModule(backups)
	plugins, e := plugin.New(db, cfg, tasks, a, metrics)
	if e != nil {
		return e
	}
	backups.SetPluginStorage(plugins)
	a.SetSSOPlugin(plugins.SSOProvider)
	a.SetPublicIP(metrics.PublicIP)
	certificates.SetDNSProviders(plugins)
	docker.SetDeploymentHooks(plugins.ComposeDeploymentHook)
	tasks.SetCompletionHook(plugins.PublishTaskEvent)
	backups.SetSiteBackupFinished(func(siteID, backupID, taskID string, err error) {
		status := "success"
		if err != nil {
			status = "failed"
		}
		plugins.PublishEvent(plugin.Event{Name: "site.backup.finished", SiteID: siteID, ResourceID: backupID, TaskID: taskID, Status: status})
	})
	certificates.SetRenewalFinished(func(certID, siteID, taskID, status string) {
		plugins.PublishEvent(plugin.Event{Name: "certificate.renewal.finished", SiteID: siteID, ResourceID: certID, TaskID: taskID, Status: status})
	})
	crons, e := cronmodule.New(db, cfg, tasks, databases, a, backups)
	if e != nil {
		plugins.Close()
		return e
	}
	defer crons.Close()
	defer plugins.Close()
	router.RegisterModule(crons)
	router.RegisterModule(oplog.New(db, a, cfg, packages, runtimeLog))
	router.RegisterModule(toolbox.New(db, tasks, a, cfg.InstallDir))
	router.RegisterModule(security)
	wafMod, e := wafmodule.New(db, cfg, packages, security)
	if e != nil {
		return e
	}
	defer wafMod.Close()
	router.RegisterModule(wafMod)
	crons.SetBlockedPorts(func() []int { return append(plugins.ManagementPorts(), cfg.Current().Port) })
	alertModule := alerts.New(db, cfg, packages, plugins.ManagementPorts)
	alertModule.SetNotificationProviders(plugins)
	alertModule.SetPluginChecks(plugins)
	router.RegisterModule(alertModule)
	router.RegisterModule(plugins)
	plugins.WebhookRoutes(router.Engine)
	plugins.FrameRoutes(router.Engine)
	if e = plugins.Start(plugin.Services{
		Alerts: alertModule, Backups: backups, Cron: crons, Docker: docker, Database: databases,
		Websites: websites, Packages: packages, Security: security, WAF: wafMod, Certificates: certificates, Files: files,
	}); e != nil {
		return e
	}
	entrance := server.NewListener(router)
	settings, e := settingsmodule.New(db, cfg, a, entrance.Apply, security.RefreshPanelPort)
	if e != nil {
		return e
	}
	router.RegisterModule(settings)
	certificates.SetRenewalDeployer(func(ctx context.Context, id string) error {
		return errors.Join(websites.ApplyRenewedCertificate(ctx, id), settings.ApplyRenewedCertificate(ctx, id))
	})
	crons.SetCertificateRenewer(certificates.RenewDue)
	crons.SetProjectRestarter(websites.RestartProject)
	if e = entrance.Apply(cfg); e != nil {
		return e
	}
	go websites.StartProjectsOnBoot(ctx)
	go packages.RunDatabaseLogRotation(ctx)
	go alertModule.Run(ctx)
	go wafMod.Run(ctx)
	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("面板已启动", "listen", addr, "dev", *dev)
	var serveErr error
	select {
	case serveErr = <-entrance.Errors:
	case <-signals.Done():
	}
	cancel()
	shutdown, finish := context.WithTimeout(context.Background(), 10*time.Second)
	defer finish()
	_ = entrance.Close(shutdown)
	return serveErr
}
func initialize(install string, dev bool) error {
	if !terminal() {
		return errors.New("初始化需要交互终端")
	}
	if _, e := os.Stat(filepath.Join(install, "data", "config.yaml")); !os.IsNotExist(e) {
		return errors.New("配置已存在，拒绝覆盖")
	}
	if _, e := os.Stat(filepath.Join(install, "data", "panel.db")); !os.IsNotExist(e) {
		return errors.New("数据库已存在，拒绝覆盖")
	}
	var cfg config.Config
	var listener net.Listener
	var e error
	for range 20 {
		cfg, e = config.New(install)
		if e != nil {
			return e
		}
		if dev {
			cfg.ListenAddr = "127.0.0.1"
		}
		listener, e = net.Listen("tcp", net.JoinHostPort(cfg.ListenAddr, strconv.Itoa(cfg.Port)))
		if e == nil {
			break
		}
	}
	if listener == nil {
		return errors.New("无法绑定初始化端口")
	}
	defer listener.Close()
	password := ""
	alphabet := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	for {
		password = ""
		for range 16 {
			n, e := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
			if e != nil {
				return e
			}
			password += string(alphabet[n.Int64()])
		}
		if auth.ValidatePassword(password) == nil {
			break
		}
	}
	h, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if e != nil {
		return e
	}
	if e = config.Save(cfg); e != nil {
		return e
	}
	db, e := store.Open(filepath.Join(cfg.DataDir(), "panel.db"))
	if e != nil {
		return e
	}
	store.SetOpLogPath(db, filepath.Join(cfg.Layout().LogDir(), "panel", "op_logs.jsonl"))
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	if e = db.Create(&store.User{Username: "admin", PasswordHash: string(h), AuthVersion: 1}).Error; e != nil {
		return e
	}
	fmt.Println("初始化完成")
	printPanelAddress(cfg)
	fmt.Printf("用户名: admin\n初始密码（仅显示一次）: %s\n", password)
	return nil
}
