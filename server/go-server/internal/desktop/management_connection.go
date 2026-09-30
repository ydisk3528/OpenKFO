package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Packagers may supply defaults with go build -ldflags -X; no private key contents are embedded.
var DefaultManagementHost, DefaultManagementUser, DefaultManagementKey string
var DefaultManagementEndpoint string

func (c *connection) validate() error {
	if c.Endpoint != "" {
		u, err := url.Parse(c.Endpoint)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/gm/api" {
			return fmt.Errorf("请输入 https://管理域名/gm/api，不包含账号、查询参数或片段")
		}
		return nil
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]*$`).MatchString(c.Host) || !regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$`).MatchString(c.User) || c.Key == "" {
		return fmt.Errorf("请输入服务器域名、SSH 用户名和密钥文件路径（域名不包含 https:// 或路径）")
	}
	if c.Port == 0 {
		c.Port = 22
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("SSH 端口必须为 1–65535")
	}
	return nil
}

func (admin *Admin) managementConnection() (connection, error) {
	if admin.GMSettings != "" {
		data, err := os.ReadFile(admin.GMSettings)
		if err != nil {
			return connection{}, err
		}
		var settings struct {
			Connection *connection `json:"management_connection"`
		}
		if err = json.Unmarshal(bytes.TrimPrefix(data, []byte{239, 187, 191}), &settings); err != nil {
			return connection{}, err
		}
		if settings.Connection != nil && (settings.Connection.Endpoint != "" || DefaultManagementEndpoint == "") {
			return *settings.Connection, nil
		}
	}
	if DefaultManagementEndpoint != "" {
		return connection{Endpoint: DefaultManagementEndpoint}, nil
	}
	if DefaultManagementHost != "" {
		return connection{Host: DefaultManagementHost, User: DefaultManagementUser, Key: DefaultManagementKey, Port: 22}, nil
	}
	data, err := os.ReadFile(filepath.Join(admin.Root, "runtime-local", "online-admin.json"))
	if err != nil {
		return connection{}, err
	}
	var c connection
	err = json.Unmarshal(bytes.TrimPrefix(data, []byte{239, 187, 191}), &c)
	return c, err
}

func (admin *Admin) connectionSettings(request Request) (any, error) {
	if request.Operation == "management_connection_get" {
		c, err := admin.managementConnection()
		c.Token = ""
		return c, err
	}
	var session json.RawMessage
	if request.Connection == nil {
		return nil, fmt.Errorf("缺少连接配置")
	}
	if err := request.Connection.validate(); err != nil {
		return nil, err
	}
	if request.Operation == "management_connection_login" {
		if request.Connection.Endpoint == "" || request.LoginAccount == "" || request.LoginPassword == "" {
			return nil, fmt.Errorf("请输入 HTTPS 接口、GM 账号和密码")
		}
		var err error
		session, err = postManagement(*request.Connection, strings.TrimSuffix(request.Connection.Endpoint, "/api")+"/login", map[string]string{"account": request.LoginAccount, "password": request.LoginPassword})
		if err != nil {
			return nil, err
		}
	}
	request.Connection.Token = ""
	if request.Connection.Endpoint == "" {
		if _, err := os.Stat(request.Connection.Key); err != nil {
			return nil, fmt.Errorf("密钥文件不存在，请选择本机的 SSH 密钥")
		}
	}
	if admin.GMSettings == "" {
		return nil, fmt.Errorf("请在 GM 目录保留 gm-settings.json 后保存连接配置")
	}
	data, err := os.ReadFile(admin.GMSettings)
	if err != nil {
		return nil, err
	}
	var settings map[string]any
	if err = json.Unmarshal(bytes.TrimPrefix(data, []byte{239, 187, 191}), &settings); err != nil {
		return nil, err
	}
	if settings == nil {
		return nil, fmt.Errorf("GM 设置文件必须为 JSON 对象")
	}
	settings["management_connection"] = request.Connection
	data, err = json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = atomicWrite(admin.GMSettings, append(data, '\n')); err != nil {
		return nil, err
	}
	if session != nil {
		return session, nil
	}
	return request.Connection, nil
}
