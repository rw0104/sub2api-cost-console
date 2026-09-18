package setup

import (
	"strings"
	"testing"
)

func TestPluginPreviewSetupRejectsProductionBeforeConnecting(t *testing.T) {
	t.Setenv("SUB2API_PLUGIN_PREVIEW", "1")
	err := TestDatabaseConnection(&DatabaseConfig{Host: "127.0.0.1", Port: 15432, DBName: "sub2api"})
	if err == nil || !strings.Contains(err.Error(), "不能连接正式库") {
		t.Fatalf("expected isolation rejection, got %v", err)
	}
	err = TestRedisConnection(&RedisConfig{Host: "127.0.0.1", Port: 16379})
	if err == nil || !strings.Contains(err.Error(), "不能连接正式缓存") {
		t.Fatalf("expected isolation rejection, got %v", err)
	}
}
