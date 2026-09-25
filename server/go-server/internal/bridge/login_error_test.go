package bridge

import (
	"errors"
	"strings"
	"testing"
)

func TestLoginFailureReasons(t *testing.T) {
	for reason, expected := range map[string]string{
		"peer_receipt_invalid_restart_game":  "旧连接凭据已失效",
		"peer_receipt_occupied_restart_game": "活动连接占用",
		"invalid_credentials":                "账号或密码错误",
		"account_already_online":             "已有活动连接",
		"client_config_mismatch":             "客户端配置与服务器不一致",
	} {
		if text := loginFailureMessage(loginRejected(reason)); !strings.Contains(text, expected) {
			t.Fatalf("%s: %s", reason, text)
		}
	}
	if text := loginFailureMessage(errors.New("private endpoint")); strings.Contains(text, "private endpoint") {
		t.Fatal("raw endpoint exposed")
	}
}

func TestRequiredUpdateMessage(t *testing.T) {
	if got := loginFailureMessage(loginRejected("client_update_required")); got != "版本过旧，请使用群里 学习资料2.zip 进行更新。" {
		t.Fatal(got)
	}
}
