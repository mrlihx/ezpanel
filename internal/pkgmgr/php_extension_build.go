package pkgmgr

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func phpExtensionDependencies(name string, apt bool) []string {
	packages := map[string][2][]string{
		"memcached": {{"libmemcached-devel", "cyrus-sasl-devel", "zlib-devel"}, {"libmemcached-dev", "libsasl2-dev", "zlib1g-dev"}},
		"imagick":   {{"ImageMagick-devel"}, {"libmagickwand-dev"}},
		"mcrypt":    {{"libmcrypt-devel"}, {"libmcrypt-dev"}},
		"yaml":      {{"libyaml-devel"}, {"libyaml-dev"}},
		"imap":      {{"libc-client-devel", "krb5-devel", "openssl-devel"}, {"libc-client2007e-dev", "libkrb5-dev", "libssl-dev"}},
		"intl":      {{"libicu-devel"}, {"libicu-dev"}},
		"xsl":       {{"libxslt-devel"}, {"libxslt1-dev"}},
		"pgsql":     {{"libpq-devel"}, {"libpq-dev"}},
		"pdo_pgsql": {{"libpq-devel"}, {"libpq-dev"}},
		"rdkafka":   {{"librdkafka-devel"}, {"librdkafka-dev"}},
		"geoip":     {{"GeoIP-devel"}, {"libgeoip-dev"}},
		"ssh2":      {{"libssh2-devel"}, {"libssh2-1-dev"}},
		"readline":  {{"readline-devel", "ncurses-devel"}, {"libreadline-dev", "libncurses-dev"}},
		"snmp":      {{"net-snmp-devel"}, {"libsnmp-dev"}},
		"ldap":      {{"openldap-devel", "cyrus-sasl-devel"}, {"libldap2-dev", "libsasl2-dev"}},
		"enchant":   {{"enchant2-devel"}, {"libenchant-2-dev"}},
		"pspell":    {{"aspell-devel"}, {"libaspell-dev"}},
		"bz2":       {{"bzip2-devel"}, {"libbz2-dev"}},
		"gmp":       {{"gmp-devel"}, {"libgmp-dev"}},
		"zmq":       {{"zeromq-devel"}, {"libzmq3-dev"}},
		"zstd":      {{"libzstd-devel"}, {"libzstd-dev"}},
		"smbclient": {{"libsmbclient-devel"}, {"libsmbclient-dev"}},
		"event":     {{"libevent-devel", "openssl-devel"}, {"libevent-dev", "libssl-dev"}},
		"swoole":    {{"openssl-devel", "libcurl-devel"}, {"libssl-dev", "libcurl4-openssl-dev"}},
	}
	row := packages[name]
	if apt {
		return row[1]
	}
	return row[0]
}
func phpExtensionConfigure(name string) ([]string, error) {
	switch name {
	case "oci8", "pdo_oci":
		for _, pattern := range []string{"/opt/ezpanel/apps/oracle/instantclient", "/opt/ezpanel/apps/oracle/instantclient*", "/opt/oracle/instantclient*", "/usr/lib/oracle/*/client64/lib"} {
			matches, _ := filepath.Glob(pattern)
			for _, path := range matches {
				if _, err := os.Stat(filepath.Join(path, "libclntsh.so")); err == nil {
					option := "--with-oci8=shared,instantclient,"
					if name == "pdo_oci" {
						option = "--with-pdo-oci=shared,instantclient,"
					}
					return []string{option + path}, nil
				}
			}
		}
		return nil, errors.New("请先安装Oracle Instant Client Basic和SDK到/opt/ezpanel/apps/oracle/instantclient或/opt/oracle/instantclient目录")
	case "mcrypt", "imagick", "yaml", "msgpack", "xsl", "pgsql", "pdo_pgsql", "readline", "snmp", "pspell", "bz2", "gmp", "zmq", "ssh2", "smbclient":
		return []string{"--with-" + strings.ReplaceAll(name, "_", "-")}, nil
	case "enchant":
		return []string{"--with-enchant"}, nil
	case "ldap":
		args := []string{"--with-ldap=/usr", "--with-ldap-sasl"}
		triplet := map[string]string{"amd64": "x86_64-linux-gnu", "arm64": "aarch64-linux-gnu"}[runtime.GOARCH]
		if _, err := os.Stat("/usr/lib/" + triplet + "/libldap.so"); err == nil {
			args = append(args, "--with-libdir=lib/"+triplet)
		} else if _, err = os.Stat("/usr/lib64/libldap.so"); err == nil {
			args = append(args, "--with-libdir=lib64")
		}
		return args, nil
	case "imap":
		return []string{"--with-imap", "--with-imap-ssl", "--with-kerberos"}, nil
	case "memcached":
		return []string{"--enable-memcached", "--enable-memcached-sasl"}, nil
	case "redis", "memcache", "apcu", "xdebug", "mongodb", "igbinary", "opcache", "fileinfo", "exif", "intl", "swoole", "xlswriter", "rdkafka", "geoip", "yaf", "phalcon", "yac", "grpc", "xhprof", "sysvshm", "sysvmsg", "calendar", "zstd", "mailparse":
		return []string{"--enable-" + name}, nil
	case "event":
		return []string{"--enable-event", "--with-event-openssl"}, nil
	}
	return nil, nil
}
