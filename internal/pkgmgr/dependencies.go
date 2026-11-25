package pkgmgr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 先查询服务器实际安装状态，包管理器只接收缺失项，避免重复刷新源和安装依赖。
func (m *Manager) missingPackages(ctx context.Context, sys System, packages []string, log func(string)) ([]string, error) {
	name, args := "rpm", []string{"-q", "--qf", "%{NAME}\n"}
	if sys.Manager == "apt-get" {
		name, args = "dpkg-query", []string{"-W", "-f=${Package}\t${Status}\n"}
	}
	bin, err := m.LookPath(name)
	if err != nil {
		return nil, err
	}
	result, err := m.Run(ctx, time.Minute, nil, bin, append(args, packages...)...)
	// dpkg-query/rpm在部分包未安装时返回1，仍会输出其余已安装项。
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if result.Truncated || (err != nil && result.ExitCode != 1) {
		return nil, errors.New("无法完整读取服务器依赖状态，未继续安装")
	}
	installed := map[string]bool{}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if sys.Manager == "apt-get" {
			pkg, status, ok := strings.Cut(line, "\t")
			if ok && status == "install ok installed" {
				installed[pkg] = true
			}
		} else {
			installed[strings.TrimSpace(line)] = true
		}
	}
	missing := []string{}
	for _, pkg := range packages {
		if !installed[pkg] {
			missing = append(missing, pkg)
		}
	}
	log(fmt.Sprintf("依赖检查：%d项已安装，%d项缺失", len(packages)-len(missing), len(missing)))
	if len(missing) > 0 {
		log("缺失依赖：" + strings.Join(missing, ", "))
	}
	return missing, nil
}
