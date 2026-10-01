package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"
)

const chatLogBase = `
timezone: UTC
display:
  widgets:
    - id: test
      type: spacer
`

func TestChatLogConfig_Defaults(t *testing.T) {
	cfg, err := config.ParseWithEnv([]byte(chatLogBase), mockGetenv(nil))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cfg.ChatLog.GetTTL(); got != 20*time.Minute {
		t.Errorf("default TTL = %v", got)
	}
	if got := cfg.ChatLog.GetMaxMessagesPerNode(); got != 50 {
		t.Errorf("default cap = %d", got)
	}
}

func TestChatLogConfig_Custom(t *testing.T) {
	cfg, err := config.ParseWithEnv([]byte(chatLogBase+"chat_log:\n  ttl_minutes: 5\n  max_messages_per_node: 12\n"), mockGetenv(nil))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cfg.ChatLog.GetTTL(); got != 5*time.Minute {
		t.Errorf("TTL = %v", got)
	}
	if got := cfg.ChatLog.GetMaxMessagesPerNode(); got != 12 {
		t.Errorf("cap = %d", got)
	}
}

func TestChatLogConfig_RejectsNonPositive(t *testing.T) {
	for _, body := range []string{"ttl_minutes: 0", "max_messages_per_node: -1"} {
		_, err := config.ParseWithEnv([]byte(chatLogBase+"chat_log:\n  "+body+"\n"), mockGetenv(nil))
		if err == nil || !strings.Contains(err.Error(), "chat_log") {
			t.Errorf("%s: expected chat_log validation error, got %v", body, err)
		}
	}
}
