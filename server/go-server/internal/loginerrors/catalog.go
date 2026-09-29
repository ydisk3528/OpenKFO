// Package loginerrors defines stable login codes independently of display text.
package loginerrors

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type Entry struct {
	Code     string `json:"code"`
	Default  string `json:"default"`
	Tip      string `json:"tip"`
	Editable bool   `json:"editable"`
}

func Catalog() []Entry {
	return []Entry{
		{Code: "client_update_required", Default: "当前游戏版本过旧。请使用新的登录器 更新到最新版本", Tip: "旧服务器拒绝登录时使用的兼容错误码；当前版本允许旧客户端进大厅，禁止开战，未使用此登录拒绝分支。", Editable: false},
		{Code: "peer_receipt_invalid_restart_game", Default: "账号密码验证已通过，但当前游戏窗口的旧连接凭据已失效。请关闭这个游戏窗口，再从启动器重新启动；其他窗口不用关闭。", Tip: "连接凭据无效或过期；关闭对应窗口重新启动，不要要求玩家重装游戏。", Editable: true},
		{Code: "peer_receipt_occupied_restart_game", Default: "账号密码验证已通过，但当前游戏窗口的连接编号被另一条活动连接占用。为避免多开串线，请关闭这个游戏窗口并从启动器重新启动。", Tip: "连接编号已被占用；检查重复窗口和旧连接残留。", Editable: true},
		{Code: "account_already_online", Default: "该账号已有活动连接，请先退出该账号的其他游戏窗口，再重试。", Tip: "服务端仍保留该账号的活动会话；检查其它窗口或连接清理。", Editable: true},
		{Code: "server_config_mismatch", Default: "服务器地图配置未同步，请联系管理员处理，无需重新下载客户端。", Tip: "地图开放配置与服务端配置校验不一致；核对地图开放配置和客户端配置哈希。", Editable: true},
		{Code: "server_config_unavailable", Default: "服务器暂时无法读取地图配置，请稍后再试或联系管理员。", Tip: "发布版本或地图开放配置读取失败；检查数据库和发布配置日志。", Editable: true},
		{Code: "server_full", Default: "服务器在线人数已满，请稍后重试或联系管理员。", Tip: "达到服务端会话上限，检查当前会话数量和 MaxSessions 配置。", Editable: true},
		{Code: "invalid_client_port", Default: "客户端连接端口无效，请关闭此游戏窗口后重新启动。", Tip: "游戏连接端口不合法；核对登录组件版本和端口配置。", Editable: true},
		{Code: "account_banned", Default: "该账号已被封禁，请联系管理员。", Tip: "账号封禁状态生效；在用户管理核对期限和原因。", Editable: true},
		{Code: "invalid_credentials", Default: "账号或密码错误，请检查当前窗口填写的账号和密码。", Tip: "认证返回拒绝；核对账号密码及账号状态，提示不要泄露账号是否存在。", Editable: true},
		{Code: "client_config_mismatch", Default: "客户端配置与服务器不一致，请在启动器中检查客户端更新。", Tip: "旧版配置哈希不匹配提示；当前服务端记录差异后继续登录，未使用此登录拒绝分支。", Editable: false},
		{Code: "rate_limited", Default: "登录尝试过于频繁，请稍后重试。", Tip: "同账号一分钟内登录次数达到限制，或限流记录容量已满。", Editable: true},
		{Code: "busy", Default: "登录服务繁忙，请稍后重试。", Tip: "密码验证并发槽已满；查看登录量和数据库响应，不影响已开战房间。", Editable: true},
		{Code: "client_already_connected", Default: "当前游戏窗口已有连接，请关闭此窗口后从启动器重新打开。", Tip: "由登录组件本地处理，当前不从服务器热更新。当前游戏窗口已有连接，请关闭此窗口后从启动器重新打开。", Editable: false},
		{Code: "logout_failed", Default: "重新登录未完成：通知旧连接退出时失败，请关闭此游戏窗口后重试。", Tip: "由登录组件本地处理，当前不从服务器热更新。重新登录未完成：通知旧连接退出时失败，请关闭此游戏窗口后重试。", Editable: false},
		{Code: "logout_timeout", Default: "重新登录未完成：等待旧连接释放超时，请稍候重试或关闭此游戏窗口。", Tip: "由登录组件本地处理，当前不从服务器热更新。重新登录未完成：等待旧连接释放超时，请稍候重试或关闭此游戏窗口。", Editable: false},
		{Code: "client_tables_not_ready", Default: "客户端资源表尚未加载完成，请稍候再登录；持续出现请检查客户端文件完整性。", Tip: "由登录组件本地处理，当前不从服务器热更新。客户端资源表尚未加载完成，请稍候再登录；持续出现请检查客户端文件完整性。", Editable: false},
		{Code: "invalid_server_response", Default: "登录服务器回复格式不正确或缺少登录结果，请联系管理员核对服务端和登录组件版本。", Tip: "由登录组件本地处理，当前不从服务器热更新。登录服务器回复格式不正确或缺少登录结果，请联系管理员核对服务端和登录组件版本。", Editable: false},
		{Code: "ready_send_failed", Default: "账号验证已通过，但提交游戏连接准备状态失败，请重新登录。", Tip: "由登录组件本地处理，当前不从服务器热更新。账号验证已通过，但提交游戏连接准备状态失败，请重新登录。", Editable: false},
		{Code: "local_token_failed", Default: "账号验证已通过，但本地登录凭据生成失败，请关闭此窗口后重试。", Tip: "由登录组件本地处理，当前不从服务器热更新。账号验证已通过，但本地登录凭据生成失败，请关闭此窗口后重试。", Editable: false},
		{Code: "local_reply_failed", Default: "账号验证已通过，但本地登录结果生成失败，请联系管理员查看日志。", Tip: "由登录组件本地处理，当前不从服务器热更新。账号验证已通过，但本地登录结果生成失败，请联系管理员查看日志。", Editable: false},
		{Code: "local_connection_closed", Default: "账号验证已通过，但游戏窗口已关闭本地登录连接，请重新启动该窗口。", Tip: "由登录组件本地处理，当前不从服务器热更新。账号验证已通过，但游戏窗口已关闭本地登录连接，请重新启动该窗口。", Editable: false},
		{Code: "server_error", Default: "服务器处理登录时发生内部错误，请联系管理员查看日志。", Tip: "数据库或服务端内部操作失败。查看同一时间 login_database_failed 日志，勿将数据库错误提示成密码错误。", Editable: true},
	}
}
func Find(code string) (Entry, bool) {
	for _, e := range Catalog() {
		if e.Code == code {
			return e, true
		}
	}
	return Entry{}, false
}
func ValidMessage(s string) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= 300 && strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' }) < 0
}
