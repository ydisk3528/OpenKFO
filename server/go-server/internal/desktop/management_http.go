package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client editing and local previews stay on the user's computer; all online writes go to HTTPS.
func localManagementOperation(op string) bool {
	if strings.HasPrefix(op, "client_config_") || strings.HasPrefix(op, "client_directory_") || strings.HasPrefix(op, "management_connection_") {
		return true
	}
	if strings.HasPrefix(op, "weapon_") && op != "weapon_settings_get" && op != "weapon_settings_save" {
		return true
	}
	switch op {
	case "catalog", "shop_images", "shop_image_status", "task_templates", "task_extended_templates", "title_catalog", "stage_requirements", "training_missions", "talisman_client_rules":
		return true
	}
	return false
}

func (admin *Admin) httpManagement(request Request, c connection) (any, error) {
	if len(c.Token) < 32 {
		return nil, fmt.Errorf("GM 未登录，请使用管理账号登录")
	}
	return postManagement(c, c.Endpoint, request)
}

func postManagement(c connection, endpoint string, input any) (json.RawMessage, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("管理接口地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := &http.Client{Timeout: 85 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTPS 管理连接失败：%v；写入结果可能未收到，请核对后使用原操作编号重试", err)
	}
	defer response.Body.Close()
	if response.StatusCode == 401 {
		if strings.HasSuffix(endpoint, "/gm/login") {
			return nil, fmt.Errorf("GM 账号或密码错误，或账号未获得管理权限")
		}
		return nil, fmt.Errorf("GM 登录会话已失效，请重新登录")
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, fmt.Errorf("管理接口发生重定向，请填写最终 HTTPS 地址")
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	if err != nil || len(data) > 16<<20 {
		return nil, fmt.Errorf("管理响应读取失败或超过 16 MB")
	}
	var result struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if json.Unmarshal(data, &result) != nil {
		return nil, fmt.Errorf("管理接口返回非 JSON 响应（HTTP %d），请检查代理路由", response.StatusCode)
	}
	if response.StatusCode != 200 || !result.OK {
		if result.Error == "" {
			result.Error = "管理请求失败"
		}
		return nil, fmt.Errorf("HTTP %d：%s", response.StatusCode, result.Error)
	}
	return result.Result, nil
}
