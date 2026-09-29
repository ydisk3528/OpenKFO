package bridge

import (
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"kungfu.local/server/internal/loginerrors"
	"net"
	"os"
	"strings"
	"syscall"
)

type loginRejected string

func (e loginRejected) Error() string { return "login rejected: " + string(e) }

type remoteLoginRejected struct {
	Code    loginRejected
	Message string
}

func (e remoteLoginRejected) Error() string { return e.Code.Error() }
func (e remoteLoginRejected) Unwrap() error { return e.Code }
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
	detail := loginFailureDetail(err)
	var remote remoteLoginRejected
	if errors.As(err, &remote) && code != "SERVER_RESPONSE_UNKNOWN" && strings.TrimSpace(remote.Message) != "" && loginerrors.ValidMessage(remote.Message) {
		detail = remote.Message
	}
	return detail + "\n错误码：" + code
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
	if entry, ok := loginerrors.Find(string(rejection)); ok {
		return entry.Default
	}
	return "登录服务器拒绝了请求，请将下方错误码提供给管理员排查。"
}
