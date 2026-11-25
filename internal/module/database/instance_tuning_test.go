package database

import (
	"strconv"
	"testing"
)

func TestDatabaseTuningUsesContainerLimitAndAvailableMemory(t *testing.T) {
	const mib = 1 << 20
	mysql := databaseTuningProfiles("mysql", 8192*mib, 4096*mib, 512*mib)
	if len(mysql) != 2 || mysql[0].Values["innodb_buffer_pool_size"] != strconv.Itoa(64*mib) || mysql[1].Values["innodb_buffer_pool_size"] != strconv.Itoa(128*mib) {
		t.Fatalf("container limit not reflected in MySQL profiles: %+v", mysql)
	}
	redis := databaseTuningProfiles("redis", 8192*mib, 128*mib, 0)
	if len(redis) != 1 || len(redis[0].Values) != 1 || redis[0].Values["maxmemory"] != strconv.Itoa(64*mib) {
		t.Fatalf("host availability or Redis eviction policy changed unexpectedly: %+v", redis)
	}
	if got := databaseTuningProfiles("mysql", 4096*mib, 16*mib, 0); len(got) != 0 {
		t.Fatalf("low memory produced a profile: %+v", got)
	}
}
