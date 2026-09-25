package bridge

import "errors"

type loginRejected string

func (e loginRejected) Error() string { return "login rejected: " + string(e) }
func loginFailureMessage(err error) string {
	var rejection loginRejected
	if !errors.As(err, &rejection) {
		return "连接登录服务失败，请检查网络后重试。"
	}
	switch string(rejection) {
	case "client_update_required":
		return "版本过旧，请使用群里 学习资料2.zip 进行更新。"
	case "peer_receipt_invalid_restart_game":
		return "账号密码验证已通过，但当前游戏窗口的旧连接凭据已失效。请关闭这个游戏窗口，再从启动器重新启动；其他窗口不用关闭。"
	case "peer_receipt_occupied_restart_game":
		return "账号密码验证已通过，但当前游戏窗口的连接编号被另一条活动连接占用。为避免多开串线，请关闭这个游戏窗口并从启动器重新启动。"
	case "account_already_online":
		return "该账号已有活动连接，请先退出该账号的其他游戏窗口，再重试。"
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
	default:
		return "登录服务暂时无法完成请求，请稍后重试。"
	}
}
