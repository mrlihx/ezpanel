package pkgmgr

import (
	"path/filepath"
	"strings"
)

type PHPExtensionOption struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Module      string `json:"module"`
	Source      string `json:"source"`
	Notice      string `json:"notice,omitempty"`
	Disabled    bool   `json:"disabled"`
}

// 显示名保留面板习惯；真实模块名和安装来源单独解析。
var phpExtensionCatalog = []PHPExtensionOption{
	{Name: "ionCube", Category: "脚本解密", Description: "用于运行ionCube Encoder加密脚本"},
	{Name: "fileinfo", Category: "通用扩展", Description: "若可用内存小于1G，可能会安装不上"},
	{Name: "opcache", Category: "缓存器", Description: "用于加速PHP脚本"},
	{Name: "memcache", Category: "缓存器", Description: "强大的内容缓存器"},
	{Name: "memcached", Category: "缓存器", Description: "比memcache支持更多高级功能"},
	{Name: "redis", Category: "缓存器", Description: "基于内存亦可持久化的Key-Value数据库"},
	{Name: "mcrypt", Category: "通用扩展", Description: "mcrypt加密/解密"},
	{Name: "apcu", Category: "缓存器", Description: "脚本缓存器"},
	{Name: "imagemagick", Category: "通用扩展", Description: "Imagick高性能图形库"},
	{Name: "xdebug", Category: "调试器", Description: "开源的PHP程序调试器"},
	{Name: "imap", Category: "邮件服务", Description: "邮件服务器必备"},
	{Name: "exif", Category: "通用扩展", Description: "用于读取图片EXIF信息"},
	{Name: "intl", Category: "通用扩展", Description: "提供国际化支持"},
	{Name: "xsl", Category: "通用扩展", Description: "xsl解析扩展"},
	{Name: "Swoole4", Category: "通用扩展", Description: "Swoole扩展共享安装状态，具体版本以phpinfo为准"},
	{Name: "xlswriter", Category: "通用扩展", Description: "xlswriter扩展"},
	{Name: "oci8", Category: "通用扩展", Description: "Oracle数据库连接扩展"},
	{Name: "pdo_oci", Category: "通用扩展", Description: "PDO Oracle数据库连接扩展"},
	{Name: "rdkafka", Category: "通用扩展", Description: "rdkafka扩展"},
	{Name: "geoip", Category: "通用扩展", Description: "Map IP address to geographic places"},
	{Name: "yaf", Category: "框架", Description: "Yaf是一个C语言编写的PHP框架"},
	{Name: "phalcon", Category: "框架", Description: "Phalcon是一个C语言编写的PHP框架"},
	{Name: "php_mongodb", Category: "通用扩展", Description: "MongoDB数据库连接驱动"},
	{Name: "yac", Category: "缓存器", Description: "高性能无锁共享内存Cache"},
	{Name: "sg11", Category: "脚本加密", Description: "SourceGuardian扩展共享安装状态，具体版本以phpinfo为准"},
	{Name: "sg14", Category: "脚本加密", Description: "SourceGuardian扩展共享安装状态，具体版本以phpinfo为准"},
	{Name: "sg15", Category: "脚本加密", Description: "SourceGuardian扩展共享安装状态，具体版本以phpinfo为准"},
	{Name: "sg16", Category: "脚本加密", Description: "SourceGuardian扩展共享安装状态，具体版本以phpinfo为准"},
	{Name: "sg17", Category: "脚本加密", Description: "SourceGuardian扩展共享安装状态，具体版本以phpinfo为准"},
	{Name: "xload", Category: "脚本加密", Description: "xload解密扩展"},
	{Name: "pgsql", Category: "通用扩展", Description: "PHP官方扩展，PostgreSQL数据库连接"},
	{Name: "ssh2", Category: "通用扩展", Description: "libssh2 PHP扩展"},
	{Name: "grpc", Category: "通用扩展", Description: "gRPC PHP扩展"},
	{Name: "xhprof", Category: "通用扩展", Description: "PHP性能分析扩展"},
	{Name: "pdo_pgsql", Category: "通用扩展", Description: "PHP官方扩展，PDO PostgreSQL连接"},
	{Name: "readline", Category: "通用扩展", Description: "PHP官方扩展"},
	{Name: "snmp", Category: "通用扩展", Description: "PHP官方扩展"},
	{Name: "ldap", Category: "通用扩展", Description: "PHP官方扩展"},
	{Name: "enchant", Category: "通用扩展", Description: "PHP官方扩展"},
	{Name: "pspell", Category: "通用扩展", Description: "PHP官方扩展"},
	{Name: "bcmath", Category: "通用扩展", Description: "PHP官方扩展，任意精度数学计算"},
	{Name: "gd", Category: "通用扩展", Description: "PHP官方扩展，图像处理库"},
	{Name: "zip", Category: "通用扩展", Description: "PHP官方扩展，ZIP压缩与解压"},
	{Name: "soap", Category: "通用扩展", Description: "PHP官方扩展，SOAP协议支持"},
	{Name: "sockets", Category: "通用扩展", Description: "PHP官方扩展，底层Socket通信"},
	{Name: "pcntl", Category: "通用扩展", Description: "PHP官方扩展，进程控制支持"},
	{Name: "bz2", Category: "通用扩展", Description: "PHP官方扩展"},
	{Name: "sysvshm", Category: "通用扩展", Description: "PHP官方扩展"},
	{Name: "calendar", Category: "通用扩展", Description: "PHP官方扩展"},
	{Name: "gmp", Category: "通用扩展", Description: "PHP官方扩展"},
	{Name: "sysvmsg", Category: "通用扩展", Description: "PHP官方扩展"},
	{Name: "igbinary", Category: "通用扩展", Description: "二进制序列化扩展"},
	{Name: "zmq", Category: "通用扩展", Description: "开源通用消息库ZeroMQ扩展"},
	{Name: "zstd", Category: "通用扩展", Description: "使用Zstandard库压缩和解压缩"},
	{Name: "smbclient", Category: "通用扩展", Description: "Samba相关功能和smb流"},
	{Name: "event", Category: "通用扩展", Description: "提供libevent库的接口"},
	{Name: "mailparse", Category: "通用扩展", Description: "邮件消息处理"},
	{Name: "yaml", Category: "通用扩展", Description: "YAML解析"},
}

func PHPExtensionModule(name string) string {
	switch name = strings.ToLower(name); name {
	case "zend opcache":
		return "opcache"
	case "ioncube loader", "ioncube":
		return "ioncube"
	case "sg11", "sg14", "sg15", "sg16", "sg17", "sourceguardian":
		return "sourceguardian"
	case "swoole4":
		return "swoole"
	case "imagemagick":
		return "imagick"
	case "php_mongodb":
		return "mongodb"
	}
	return name
}
func phpExtensionSource(id, module string) string {
	switch module {
	case "bcmath", "fileinfo", "opcache", "exif", "intl", "xsl", "pgsql", "pdo_pgsql", "readline", "snmp", "ldap", "enchant", "bz2", "sysvshm", "sysvsem", "sysvmsg", "calendar", "gmp", "gd", "zip", "soap", "sockets", "pcntl", "shmop", "gettext", "dba", "ftp", "tidy", "ffi", "mysqli", "pdo_mysql":
		return "php"
	case "imap", "pspell":
		if phpBranch(id) < "8.4" {
			return "php"
		}
	case "pdo_oci":
		if phpBranch(id) < "8.3" {
			return "php"
		}
	case "ioncube", "sourceguardian":
		return "loader"
	case "xload":
		return "manual"
	}
	return "pecl"
}
func PHPExtensionCatalog(id string) []PHPExtensionOption {
	rows := append([]PHPExtensionOption(nil), phpExtensionCatalog...)
	for i := range rows {
		row := &rows[i]
		row.Module = PHPExtensionModule(row.Name)
		row.Source = phpExtensionSource(id, row.Module)
		switch row.Module {
		case "sourceguardian":
			row.Notice = "安装当前官方SourceGuardian加载器，兼容SG8–17；这些选项共享实际模块。"
		case "swoole":
			row.Notice = "此入口安装Swoole 4.x；其他版本可通过PECL安装，状态按实际swoole模块显示。"
		case "oci8", "pdo_oci":
			row.Notice = "需先安装Oracle Instant Client Basic和SDK，支持/opt/ezpanel/apps/oracle/instantclient或/opt/oracle/instantclient*。"
		case "xload":
			row.Notice = "尚无可验证的厂商Linux安装包来源，请按厂商文档手动安装。"
			row.Disabled = true
		}
	}
	return rows
}

func (m *Manager) phpExtensionINI(id, name string) string {
	order := "50"
	if name == "ioncube" {
		order = "00"
	}
	return filepath.Join(m.phpDirectory(id), "conf.d", order+"-panel-"+name+".ini")
}
