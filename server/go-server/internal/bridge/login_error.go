package bridge

import (
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
)

type loginRejected string

func (e loginRejected) Error() string { return "login rejected: " + string(e) }
func loginFailureMessage(err error) string {
	code := "CONNECTION_UNKNOWN"
	var rejection loginRejected
	if errors.As(err, &rejection) {
		code = string(rejection)
		if len(code) == 0 || len(code) > 80 || strings.IndexFunc(code, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') }) >= 0 {
			code = "SERVER_RESPONSE_UNKNOWN"
		}
	} else {
		var dns *net.DNSError
		var network net.Error
		var file *os.PathError
		switch {
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			code = "CONNECTION_CLOSED"
		case errors.As(err, &dns):
			code = "DNS_FAILED"
		case errors.As(err, &file):
			code = "LOCAL_FILE_UNAVAILABLE"
		case errors.As(err, &network) && network.Timeout():
			code = "LOGIN_TIMEOUT"
		case strings.Contains(err.Error(), "certificate"):
			code = "CERTIFICATE_FAILED"
		default:
			var n syscall.Errno
			if errors.As(err, &n) {
				code = fmt.Sprintf("SOCKET_%d", n)
			}
		}
	}
	return loginFailureDetail(err) + "\n错误码：" + code
}
func loginFailureDetail(err error) string {
	var rejection loginRejected
	if !errors.As(err, &rejection) {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return "登录连接已被关闭，未收到服务器回复（EOF）。可能是服务端连接限制或连接中断，不能据此判定为本机网络故障。请稍后重试，持续发生请联系管理员查看服务端日志。"
		}
		var dns *net.DNSError
		if errors.As(err, &dns) {
			return "登录服务器域名解析失败。请检查 DNS 或联系管理员确认服务器地址。"
		}
		var unknownCA x509.UnknownAuthorityError
		var hostname x509.HostnameError
		if errors.As(err, &unknownCA) || errors.As(err, &hostname) || strings.Contains(err.Error(), "certificate") {
			return "登录服务器证书验证失败。请检查电脑时间，并使用配套的启动器和证书文件。"
		}
		var network net.Error
		if errors.As(err, &network) && network.Timeout() {
			return "连接或等待登录服务器回复超时。可能是服务器繁忙或线路异常，请稍后重试。"
		}
		var file *os.PathError
		if errors.As(err, &file) {
			return "无法读取登录所需的本地文件，请检查启动器配套文件是否完整以及访问权限。"
		}
		var socket syscall.Errno
		if errors.As(err, &socket) {
			switch uint64(socket) {
			case 10061, 111:
				return "服务器拒绝连接，登录端口未接受请求，请联系管理员确认服务器已启动。"
			case 10054, 104:
				return "登录连接被服务器或中间网络重置，请重试；持续发生请联系管理员查看日志。"
			case 10051, 10065, 101, 113:
				return "无法到达服务器网络，请检查网络连接或线路。"
			}
		}
		return "登录连接失败，尚未确定原因。请导出日志交给管理员排查；此提示不代表账号密码错误或本机网络故障。"
	}
	switch string(rejection) {
	case "client_update_required":
		return "当前游戏版本过旧。请使用新的登录器 更新到最新版本"
	case "peer_receipt_invalid_restart_game":
		return "账号密码验证已通过，但当前游戏窗口的旧连接凭据已失效。请关闭这个游戏窗口，再从启动器重新启动；其他窗口不用关闭。"
	case "peer_receipt_occupied_restart_game":
		return "账号密码验证已通过，但当前游戏窗口的连接编号被另一条活动连接占用。为避免多开串线，请关闭这个游戏窗口并从启动器重新启动。"
	case "account_already_online":
		return "该账号已有活动连接，请先退出该账号的其他游戏窗口，再重试。"
	case "server_config_mismatch":
		return "服务器地图配置未同步，请联系管理员处理，无需重新下载客户端。"
	case "server_config_unavailable":
		return "服务器暂时无法读取地图配置，请稍后再试或联系管理员。"
	case "server_full":
		return "服务器在线人数已满，请稍后重试或联系管理员。"
	case "invalid_client_port":
		return "客户端连接端口无效，请关闭此游戏窗口后重新启动。"
	case "account_banned":
		return "该账号已被封禁，请联系管理员。"
	case "invalid_credentials":
		return "账号或密码错误，请检查当前窗口填写的账号和密码。"
	case "client_config_mismatch":
		return "客户端配置与服务器不一致，请在启动器中检查客户端更新。"
	case "rate_limited":
		return "登录尝试过于频繁，请稍后重试。"
	case "busy":
		return "登录服务繁忙，请稍后重试。"
	case "client_already_connected":
		return "当前游戏窗口已有连接，请关闭此窗口后从启动器重新打开。"
	case "logout_failed":
		return "重新登录未完成：通知旧连接退出时失败，请关闭此游戏窗口后重试。"
	case "logout_timeout":
		return "重新登录未完成：等待旧连接释放超时，请稍候重试或关闭此游戏窗口。"
	case "client_tables_not_ready":
		return "客户端资源表尚未加载完成，请稍候再登录；持续出现请检查客户端文件完整性。"
	case "invalid_server_response":
		return "登录服务器回复格式不正确或缺少登录结果，请联系管理员核对服务端和登录组件版本。"
	case "ready_send_failed":
		return "账号验证已通过，但提交游戏连接准备状态失败，请重新登录。"
	case "local_token_failed":
		return "账号验证已通过，但本地登录凭据生成失败，请关闭此窗口后重试。"
	case "local_reply_failed":
		return "账号验证已通过，但本地登录结果生成失败，请联系管理员查看日志。"
	case "local_connection_closed":
		return "账号验证已通过，但游戏窗口已关闭本地登录连接，请重新启动该窗口。"
	case "server_error":
		return "服务器处理登录时发生内部错误，请联系管理员查看日志。"
	default:
		return "登录服务器拒绝了请求，请将下方错误码提供给管理员排查。"
	}
}
