package bridge

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"
)

func TestLoginFailureReasons(t *testing.T) {
	for reason, expected := range map[string]string{
		"peer_receipt_invalid_restart_game":  "旧连接凭据已失效",
		"peer_receipt_occupied_restart_game": "活动连接占用",
		"server_full":                        "在线人数已满",
		"invalid_client_port":                "连接端口无效",
		"invalid_credentials":                "账号或密码错误",
		"account_already_online":             "已有活动连接",
		"client_config_mismatch":             "客户端配置与服务器不一致",
		"rate_limited":                       "登录尝试过于频繁",
		"busy":                               "登录服务繁忙",
		"server_error":                       "内部错误",
	} {
		if text := loginFailureMessage(loginRejected(reason)); !strings.Contains(text, expected) {
			t.Fatalf("%s: %s", reason, text)
		}
	}
	for _, item := range []struct {
		err  error
		want string
	}{
		{io.EOF, "未收到服务器回复"},
		{io.ErrUnexpectedEOF, "未收到服务器回复"},
		{&net.DNSError{Err: "not found", Name: "private endpoint"}, "域名解析失败"},
		{&net.OpError{Op: "read", Err: &net.DNSError{IsTimeout: true}}, "域名解析失败"},
	} {
		if got := loginFailureMessage(item.err); !strings.Contains(got, item.want) {
			t.Fatalf("%v: %s", item.err, got)
		}
	}
	if text := loginFailureMessage(errors.New("private endpoint")); strings.Contains(text, "private endpoint") {
		t.Fatal("raw endpoint exposed")
	}
}

func TestRequiredUpdateMessage(t *testing.T) {
	if got := loginFailureMessage(loginRejected("client_update_required")); got != "当前游戏版本过旧。请使用新的登录器 更新到最新版本\n错误码：client_update_required" {
		t.Fatal(got)
	}
}

func TestSpecificLoginFailureCodes(t *testing.T) {
	for reason, detail := range map[string]string{
		"client_already_connected": "已有连接", "logout_failed": "旧连接退出", "logout_timeout": "释放超时", "client_tables_not_ready": "资源表", "invalid_server_response": "回复格式", "ready_send_failed": "准备状态", "local_token_failed": "凭据生成", "local_reply_failed": "结果生成", "local_connection_closed": "本地登录连接", "account_banned": "封禁", "server_config_mismatch": "地图配置未同步", "server_config_unavailable": "读取地图配置", "future_reason": "拒绝了请求",
	} {
		got := loginFailureMessage(loginRejected(reason))
		if !strings.Contains(got, detail) || !strings.Contains(got, "错误码："+reason) || strings.Contains(got, "功夫小子") {
			t.Fatalf("%s: %s", reason, got)
		}
	}
}

func TestRemoteLoginText(t *testing.T) {
	for _, tc := range []struct{ message, want string }{
		{"请联系值班管理员", "请联系值班管理员\n错误码：server_error"},
		{"", loginFailureMessage(loginRejected("server_error"))},
		{strings.Repeat("字", 301), loginFailureMessage(loginRejected("server_error"))},
		{"错误\x00伪造", loginFailureMessage(loginRejected("server_error"))},
	} {
		if got := loginFailureMessage(remoteLoginRejected{Code: "server_error", Message: tc.message}); got != tc.want {
			t.Fatalf("got %q want %q", got, tc.want)
		}
	}
	if got := loginFailureMessage(remoteLoginRejected{Code: "bad\ncode", Message: "覆盖"}); strings.Contains(got, "覆盖") {
		t.Fatal(got)
	}
}
