package database

import (
	"context"
	"errors"
	"strconv"

	"github.com/shirou/gopsutil/v3/mem"
)

type databaseTuningProfile struct {
	ID     string            `json:"id"`
	Values map[string]string `json:"values"`
}

type databaseTuning struct {
	HostMemory    uint64                  `json:"hostMemory"`
	Available     uint64                  `json:"available"`
	InstanceLimit uint64                  `json:"instanceLimit"`
	Profiles      []databaseTuningProfile `json:"profiles"`
}

func recommendedDatabaseTuning(ctx context.Context, engine string, instanceLimit uint64) (databaseTuning, error) {
	host, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil || host.Total == 0 {
		return databaseTuning{}, errors.New("无法读取服务器内存，请稍后重试")
	}
	return databaseTuning{
		HostMemory: host.Total, Available: host.Available, InstanceLimit: instanceLimit,
		Profiles: databaseTuningProfiles(engine, host.Total, host.Available, instanceLimit),
	}, nil
}

func databaseTuningProfiles(engine string, host, available, instanceLimit uint64) []databaseTuningProfile {
	const mib = 1 << 20
	budget := host
	if instanceLimit > 0 {
		budget = min(budget, instanceLimit)
	}
	profiles := []databaseTuningProfile{}
	var last uint64
	for _, profile := range []struct {
		id      string
		divisor uint64
	}{{"conservative", 8}, {"balanced", 4}} {
		// 共享宿主机为系统和其他实例保留内存，单个容器限额也参与计算。
		target := min(budget/profile.divisor, available/2, 32<<30) / mib * mib
		if target < 32*mib || target == last {
			continue
		}
		last = target
		values := map[string]string{"maxmemory": strconv.FormatUint(target, 10)}
		if engine == "mysql" {
			values = map[string]string{
				"innodb_buffer_pool_size": strconv.FormatUint(target, 10),
				"innodb_log_buffer_size":  strconv.FormatUint(min(64, max(8, target/mib/32))*mib, 10),
			}
		}
		profiles = append(profiles, databaseTuningProfile{ID: profile.id, Values: values})
	}
	return profiles
}
