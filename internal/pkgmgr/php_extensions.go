package pkgmgr

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/executor"
	"golang.org/x/text/encoding/charmap"
	"io"
)

var phpExtensionName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,49}$`)
var phpExtensionVersion = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){1,3}(?:[a-zA-Z][a-zA-Z0-9]*)?$`)

func ValidPHPExtension(name, version string) bool {
	return phpExtensionName.MatchString(name) && (version == "" || phpExtensionVersion.MatchString(version))
}

func decodePECL(data []byte, v any) error {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	decoder.CharsetReader = func(label string, r io.Reader) (io.Reader, error) {
		if strings.EqualFold(label, "iso-8859-1") {
			return charmap.ISO8859_1.NewDecoder().Reader(r), nil
		}
		return nil, errors.New("不支持的PECL元数据编码")
	}
	return decoder.Decode(v)
}
func phpVersionLess(a, b string) bool {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		x, y := 0, 0
		if i < len(left) {
			x, _ = strconv.Atoi(left[i])
		}
		if i < len(right) {
			y, _ = strconv.Atoi(right[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}
func (m *Manager) peclVersion(ctx context.Context, name, version, php string, major4 bool) (string, error) {
	versions := []string{version}
	if version == "" {
		data, err := m.sourceRequest(ctx, "https://pecl.php.net/rest/r/"+name+"/allreleases.xml", 4<<20)
		if err != nil {
			return "", err
		}
		var index struct {
			Releases []struct {
				Version string `xml:"v"`
				State   string `xml:"s"`
			} `xml:"r"`
		}
		if err = decodePECL(data, &index); err != nil {
			return "", err
		}
		versions = nil
		for _, r := range index.Releases {
			if (r.State == "stable" || (name == "geoip" || name == "zmq") && r.State == "beta") && phpExtensionVersion.MatchString(r.Version) && (!major4 || strings.HasPrefix(r.Version, "4.")) {
				versions = append(versions, r.Version)
			}
		}
	}
	for _, candidate := range versions {
		data, err := m.sourceRequest(ctx, "https://pecl.php.net/rest/r/"+name+"/package."+candidate+".xml", 8<<20)
		if err != nil {
			return "", err
		}
		var metadata struct {
			Name         string `xml:"name"`
			Dependencies struct {
				Required struct {
					PHP struct {
						Min     string   `xml:"min"`
						Max     string   `xml:"max"`
						Exclude []string `xml:"exclude"`
					} `xml:"php"`
				} `xml:"required"`
			} `xml:"dependencies"`
		}
		if err = decodePECL(data, &metadata); err != nil {
			return "", err
		}
		constraint := metadata.Dependencies.Required.PHP
		if !strings.EqualFold(metadata.Name, name) {
			return "", errors.New("PECL软件包名称不匹配")
		}
		if (constraint.Min != "" && phpVersionLess(php, constraint.Min)) || (constraint.Max != "" && phpVersionLess(constraint.Max, php)) || contains(constraint.Exclude, php) {
			continue
		}
		return candidate, nil
	}
	return "", errors.New("没有兼容当前PHP版本的PECL稳定版本")
}

func (m *Manager) ChangePHPExtension(ctx context.Context, id, name, version, action string, log func(string)) error {
	if err := m.lock(id); err != nil {
		return err
	}
	defer m.unlock(id)
	return m.changePHPExtension(ctx, id, name, version, action, log)
}
func (m *Manager) changePHPExtension(ctx context.Context, id, requested, version, action string, log func(string)) error {
	if log == nil {
		log = func(string) {}
	}
	name := PHPExtensionModule(requested)
	source := phpExtensionSource(id, name)
	if source == "manual" {
		return errors.New("该扩展尚无可验证的厂商安装源，请按厂商文档手动安装")
	}
	if UsesDocker(id) {
		if _, err := m.sourceRecord(id); os.IsNotExist(err) {
			return m.changeDockerPHPExtension(ctx, id, requested, name, source, version, action, log)
		}
	}
	record, err := m.sourceRecord(id)
	if err != nil {
		return err
	}
	rows, err := m.PHPExtensions(ctx, id)
	if err != nil {
		return err
	}
	ini := m.phpExtensionINI(id, name)
	loaded := false
	for _, r := range rows {
		normalized := PHPExtensionModule(r.Name)
		if normalized == name {
			loaded = true
			if action == "uninstall" && (r.Builtin || !r.Managed) {
				return errors.New("该扩展为内置或由其他配置加载，请在配置文件中管理")
			}
		}
	}
	if action == "uninstall" {
		content, err := os.ReadFile(ini)
		if err != nil {
			return err
		}
		if err = os.WriteFile(ini+".panel-backup", content, 0600); err != nil {
			return err
		}
		if err = os.Remove(ini); err != nil {
			return err
		}
		log("已移除当前PHP版本的扩展加载配置；重载PHP后生效，扩展二进制保留")
		return nil
	}
	if loaded {
		return errors.New("当前PHP已加载该扩展")
	}
	if name == "phalcon" {
		psr := false
		for _, row := range rows {
			psr = psr || row.Module == "psr"
		}
		if !psr {
			log("先安装Phalcon依赖psr")
			if err = m.changePHPExtension(ctx, id, "psr", "", "install", log); err != nil {
				return err
			}
		}
	}
	if source == "loader" {
		return m.installPHPLoader(ctx, id, name, record, log)
	}
	sys, err := m.Detect()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.layout().TempDir(), 0700); err != nil {
		return err
	}
	work, err := os.MkdirTemp(m.layout().TempDir(), "php-extension-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	env := phpEnvironment(id, record.Prefix, executor.DefaultEnv())
	run := func(cwd, bin string, args ...string) error {
		if !filepath.IsAbs(bin) {
			var e error
			bin, e = m.LookPath(bin)
			if e != nil {
				return e
			}
		}
		_, e := executor.StreamFixed(ctx, 2*time.Hour, cwd, env, log, bin, args...)
		return e
	}
	err = func() error {
		m.packageMu.Lock()
		defer m.packageMu.Unlock()
		deps := []string{"gcc", "gcc-c++", "make", "autoconf", "automake", "libtool", "pkgconf-pkg-config"}
		if sys.Manager == "apt-get" {
			deps = []string{"build-essential", "autoconf", "automake", "libtool", "pkg-config"}
		}
		extra := phpExtensionDependencies(name, sys.Manager == "apt-get")
		deps = append(deps, extra...)
		missing, e := m.missingPackages(ctx, sys, deps, log)
		if e != nil || len(missing) == 0 {
			return e
		}
		if sys.Manager == "apt-get" {
			if _, e = m.Stream(ctx, 15*time.Minute, append(executor.DefaultEnv(), "DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=l"), log, sys.Command, "update"); e != nil {
				return e
			}
		}
		if e = m.checkPlan(ctx, sys, "install", missing, nil, log); e != nil {
			return e
		}
		args := []string{"-y", "--setopt=obsoletes=False", "install"}
		if sys.Manager == "apt-get" {
			args = []string{"-y", "--no-remove", "--no-upgrade", "install"}
		}
		_, e = m.Stream(ctx, 30*time.Minute, append(executor.DefaultEnv(), "DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=l"), log, sys.Command, append(args, missing...)...)
		return e
	}()
	if err != nil {
		return err
	}
	var src string
	if source == "php" {
		src, err = m.compatSource(ctx, work, "php-"+record.Version, record.URL, record.SHA256, log)
		src = filepath.Join(src, "ext", name)
	} else {
		version, err = m.peclVersion(ctx, name, version, record.Version, strings.EqualFold(requested, "swoole4"))
		if err != nil {
			return err
		}
		log("安装官方PECL扩展：" + name + "-" + version + " → " + id)
		src, err = m.compatSource(ctx, work, name, "https://pecl.php.net/get/"+name+"-"+version+".tgz", "", log, true)
	}
	if err != nil {
		return err
	}
	if err = run(src, record.Prefix+"/bin/phpize"); err != nil {
		return err
	}
	args := []string{"./configure", "--with-php-config=" + record.Prefix + "/bin/php-config"}
	flags, e := phpExtensionConfigure(name)
	if e != nil {
		return e
	}
	args = append(args, flags...)
	if err = run(src, "sh", args...); err != nil {
		return err
	}
	if err = run(src, "make", "-j2"); err != nil {
		return err
	}
	if err = run(src, "make", "install"); err != nil {
		return err
	}
	directive := "extension"
	if name == "xdebug" || name == "opcache" {
		directive = "zend_extension"
	}
	// 先用独立CLI进程检查ABI和加载结果，失败时不改正在运行的FPM。
	moduleName := name
	if name == "opcache" {
		moduleName = "Zend OPcache"
	}
	check, err := m.Run(ctx, 15*time.Second, env, record.Prefix+"/bin/php", "-c", m.phpDirectory(id)+"/php.ini", "-d", directive+"="+name+".so", "-r", fmt.Sprintf(`exit(extension_loaded(%q)?0:1);`, moduleName))
	if err != nil {
		return fmt.Errorf("扩展加载检查失败：%s %s", check.Stdout, check.Stderr)
	}
	if old, e := os.ReadFile(ini); e == nil {
		if err = os.WriteFile(ini+".panel-backup", old, 0600); err != nil {
			return err
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if err = os.MkdirAll(filepath.Dir(ini), 0755); err != nil {
		return err
	}
	if err = os.WriteFile(ini, []byte(directive+"="+name+".so\n"), 0644); err != nil {
		return err
	}
	if err = os.Chmod(ini, 0644); err != nil {
		return err
	}
	log("扩展已安装；请重载PHP服务使网站生效")
	return nil
}
