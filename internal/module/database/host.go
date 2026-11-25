package database

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strings"

	"ezpanel/internal/store"
)

// isMySQLNetmaskHost 判断 host 是否为合法的 MySQL IPv4/子网掩码格式（如 172.18.0.0/255.255.0.0）。
func isMySQLNetmaskHost(host string) bool {
	_, ok := parseIPv4NetmaskHost(host)
	return ok
}

func parseIPv4NetmaskHost(raw string) (string, bool) {
	ipPart, maskPart, ok := strings.Cut(raw, "/")
	if !ok || ipPart == "" || maskPart == "" {
		return "", false
	}
	ip := net.ParseIP(ipPart).To4()
	maskIP := net.ParseIP(maskPart).To4()
	if ip == nil || maskIP == nil {
		return "", false
	}
	mask := net.IPMask(maskIP)
	ones, bits := mask.Size()
	if bits != 32 || ones == 0 {
		return "", false
	}
	return ip.Mask(mask).String() + "/" + maskIP.String(), true
}

// normalizeAccessHost 校验并规范化数据库访问权限 host，支持 localhost、%、单 IP、IPv4 CIDR 及 MySQL IP/Netmask。
func normalizeAccessHost(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" || v == "localhost" || v == "local" {
		return "localhost", nil
	}
	if v == "%" {
		return "%", nil
	}
	if ip := net.ParseIP(v); ip != nil {
		return ip.String(), nil
	}
	if strings.Contains(v, "/") {
		if _, ipNet, err := net.ParseCIDR(v); err == nil {
			ip4 := ipNet.IP.To4()
			mask4 := net.IP(ipNet.Mask).To4()
			if ip4 != nil && mask4 != nil {
				ones, bits := ipNet.Mask.Size()
				if bits == 32 && ones > 0 {
					return ip4.String() + "/" + mask4.String(), nil
				}
			}
		}
		if normalized, ok := parseIPv4NetmaskHost(v); ok {
			return normalized, nil
		}
	}
	return "", errors.New("请选择本地服务器、所有人或填写有效IP地址或IPv4网段（如 192.168.1.0/24）")
}

// containerLocalSubnet 返回容器化 MySQL 实例对应的 ezpanel-network 子网（IP/Netmask 格式）。
// 非容器实例返回 ("localhost", false, nil)。
func (m *Module) containerLocalSubnet(ctx context.Context, s store.DBServer) (string, bool, error) {
	if !usesContainerClient(s) || m.packages == nil {
		return "localhost", false, nil
	}
	subnet, err := m.packages.ManagedNetworkSubnet(ctx)
	if err != nil {
		return "", true, errors.New("无法读取容器内网网段，请确认 Docker 与 ezpanel-network 正常")
	}
	return subnet, true, nil
}

// resolveAccessTargets 将用户选择的访问权限解析为：
//   - primary：存入面板元数据并作为主授权的 host；
//   - companion：容器化 MySQL 在「指定IP」模式下自动维护的伴生容器内网 host（保证宿主机与同机容器始终可连）；
//   - localSubnet：容器内网子网（非容器时为 "localhost"）；
//   - isContainer：是否为容器化 MySQL 实例。
func (m *Module) resolveAccessTargets(ctx context.Context, s store.DBServer, requested string) (primary, companion, localSubnet string, isContainer bool, err error) {
	norm, err := normalizeAccessHost(requested)
	if err != nil {
		return "", "", "", false, err
	}
	localSubnet, isContainer, err = m.containerLocalSubnet(ctx, s)
	if err != nil {
		return "", "", "", true, err
	}
	if !isContainer {
		return norm, "", "localhost", false, nil
	}
	if norm == "localhost" || norm == localSubnet {
		return localSubnet, "", localSubnet, true, nil
	}
	if norm == "%" {
		return "%", "", localSubnet, true, nil
	}
	return norm, localSubnet, localSubnet, true, nil
}

func mysqlUserExists(ctx context.Context, db *sql.DB, user, host string) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM mysql.user WHERE User = ? AND Host = ?", user, host).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func dbGrantPattern(ctx context.Context, db *sql.DB, dbName string) (string, error) {
	var variable, partial string
	err := db.QueryRowContext(ctx, "SHOW VARIABLES LIKE 'partial_revokes'").Scan(&variable, &partial)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("无法确认库级授权匹配规则")
	}
	if !strings.EqualFold(partial, "ON") && partial != "1" {
		return strings.ReplaceAll(dbName, "_", "\\_"), nil
	}
	return dbName, nil
}

// ensureCompanionUser 在容器化 MySQL 上确保伴生内网账号存在、密码一致且持有目标库全部权限。
func ensureCompanionUser(ctx context.Context, db *sql.DB, user, companionHost, password, grantName string) error {
	exists, err := mysqlUserExists(ctx, db, user, companionHost)
	if err != nil {
		return errors.New("检查容器内网伴生账号失败")
	}
	if !exists {
		if password == "" {
			return errors.New("面板未保存该账号密码，请先重设数据库密码后再修改访问权限")
		}
		if _, err := db.ExecContext(ctx, "CREATE USER ?@? IDENTIFIED BY ?", user, companionHost, password); err != nil {
			return errors.New("创建容器内网伴生账号失败，请检查数据库权限与密码策略")
		}
	} else if password != "" {
		if _, err := db.ExecContext(ctx, "ALTER USER ?@? IDENTIFIED BY ?", user, companionHost, password); err != nil {
			return errors.New("同步容器内网伴生账号密码失败")
		}
	}
	if _, err := db.ExecContext(ctx, "GRANT ALL PRIVILEGES ON `"+grantName+"`.* TO ?@?", user, companionHost); err != nil {
		return errors.New("容器内网伴生账号授权失败")
	}
	return nil
}

// dropCompanionIfExclusive 在不再需要伴生内网账号（例如切换为所有人 %）时，仅在其不关联其他库时清理。
func (m *Module) dropCompanionIfExclusive(ctx context.Context, db *sql.DB, serverID, dbName, user, companionHost, grantName string) {
	exists, err := mysqlUserExists(ctx, db, user, companionHost)
	if err != nil || !exists {
		return
	}
	var shared int64
	if err := m.db.Model(&store.DatabaseMetadata{}).
		Where("server_id = ? AND username = ? AND name <> ?", serverID, user, dbName).
		Count(&shared).Error; err != nil || shared != 0 {
		return
	}
	rows, err := db.QueryContext(ctx, "SHOW GRANTS FOR ?@?", user, companionHost)
	if err != nil {
		return
	}
	exclusive := true
	for rows.Next() {
		var grant string
		if err := rows.Scan(&grant); err != nil {
			exclusive = false
			break
		}
		upper := strings.ToUpper(grant)
		if strings.HasPrefix(upper, "GRANT USAGE ON *.* TO ") {
			continue
		}
		start, end := strings.Index(upper, " ON "), strings.LastIndex(upper, " TO ")
		if start < 0 || end <= start+4 {
			exclusive = false
			break
		}
		scope := grant[start+4 : end]
		scope = strings.TrimPrefix(strings.TrimPrefix(scope, "PROCEDURE "), "FUNCTION ")
		if scope != "`"+grantName+"`.*" && !strings.HasPrefix(scope, "`"+dbName+"`.`") {
			exclusive = false
			break
		}
	}
	if rows.Err() != nil {
		exclusive = false
	}
	rows.Close()
	if !exclusive {
		return
	}
	_, _ = db.ExecContext(ctx, "DROP USER IF EXISTS ?@?", user, companionHost)
}
