package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"kungfu.local/server/internal/gmversion"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

type Request struct {
	LoginErrors            *persistence.LoginErrorSettings      `json:"login_errors,omitempty"`
	Treasure               *persistence.TreasureSettings        `json:"treasure,omitempty"`
	ClientConfig           *clientConfigRequest                 `json:"client_config,omitempty"`
	RecommendationPriority int32                                `json:"recommendation_priority"`
	PinRecommended         bool                                 `json:"pin_recommended"`
	BatchItems             []persistence.BatchGrantItem         `json:"batch_items,omitempty"`
	UIDs                   []uint64                             `json:"uids,omitempty"`
	Reason                 string                               `json:"reason"`
	BannedWords            *persistence.BannedWordsSettings     `json:"banned_words,omitempty"`
	GMVersion              string                               `json:"gm_version"`
	Recommended            *bool                                `json:"recommended,omitempty"`
	Notes                  string                               `json:"notes,omitempty"`
	Definition             *persistence.ItemDefinition          `json:"definition,omitempty"`
	StageUnlocks           *persistence.StagePlayerUnlocks      `json:"stage_unlocks,omitempty"`
	WeaponSettings         *persistence.WeaponSettings          `json:"weapon_settings,omitempty"`
	VIPShopSettings        *persistence.VIPShopSettings         `json:"vip_shop_settings,omitempty"`
	TalismanSettings       *persistence.TalismanSettings        `json:"talisman_settings,omitempty"`
	Titles                 *persistence.TitleSettings           `json:"titles,omitempty"`
	Tasks                  *persistence.TaskSettings            `json:"tasks,omitempty"`
	Training               *persistence.TrainingSettings        `json:"training,omitempty"`
	VIPKind                uint32                               `json:"vip_kind,omitempty"`
	Honour                 *persistence.HonourSettings          `json:"honour,omitempty"`
	StageAccess            *persistence.StageAccess             `json:"stage_access,omitempty"`
	ServerExpiryDays       *uint32                              `json:"server_expiry_days,omitempty"`
	Instance               uint32                               `json:"instance"`
	ExpiresAt              *int64                               `json:"expires_at,omitempty"`
	Environment            string                               `json:"environment"`
	Rewards                *persistence.RewardRules             `json:"rewards,omitempty"`
	RewardRevision         uint64                               `json:"reward_revision"`
	Operation              string                               `json:"operation"`
	ID                     string                               `json:"id"`
	UID                    uint64                               `json:"uid"`
	Mode                   string                               `json:"mode"`
	Amount                 uint32                               `json:"amount"`
	Keys                   []string                             `json:"keys"`
	Key                    string                               `json:"key"`
	Quantity               int                                  `json:"quantity"`
	Days                   int                                  `json:"days"`
	Currency               string                               `json:"currency"`
	Price                  int64                                `json:"price"`
	Enabled                *bool                                `json:"enabled"`
	All                    bool                                 `json:"all"`
	Weapon                 int                                  `json:"weapon"`
	Revision               string                               `json:"revision"`
	Rules                  []Rule                               `json:"rules"`
	Donor                  int                                  `json:"donor"`
	Stage                  int                                  `json:"stage"`
	Action                 string                               `json:"action"`
	PropertyID             string                               `json:"property_id"`
	Label                  string                               `json:"label,omitempty"`
	SourcePath             string                               `json:"source_path,omitempty"`
	Template               string                               `json:"template"`
	TemplateWeapon         int                                  `json:"template_weapon"`
	TemplateStage          int                                  `json:"template_stage"`
	Transitions            []ComboTransition                    `json:"transitions"`
	FrameSwitches          map[int][]FrameSwitch                `json:"frame_switches,omitempty"`
	Counters               map[int]*CounterEdit                 `json:"counters,omitempty"`
	BlockElements          map[int]map[string][]BlockElement    `json:"block_elements,omitempty"`
	Scopes                 map[int]map[string][]FrameSwitchAttr `json:"scopes,omitempty"`
	ComboRule              *ComboRuleSet                        `json:"combo_rule,omitempty"`
	Target                 string                               `json:"target"`
	Path                   string                               `json:"path"`
	Directory              string                               `json:"directory"`
	Blueprint              *Blueprint                           `json:"blueprint,omitempty"`
	Include                []string                             `json:"include,omitempty"`
	AppliedOnly            bool                                 `json:"applied_only,omitempty"`
}
type Admin struct {
	Root          string
	LocalSettings string
	Remote        func(persistence.AdminRequest) (json.RawMessage, error)
}

func New(root string) *Admin { admin := &Admin{Root: root}; admin.Remote = admin.remote; return admin }

type connection struct {
	Host string `json:"host"`
	User string `json:"user"`
	Key  string `json:"key"`
	Port int    `json:"port"`
}

func (admin *Admin) remote(request persistence.AdminRequest) (json.RawMessage, error) {
	data, err := os.ReadFile(filepath.Join(admin.Root, "runtime-local", "online-admin.json"))
	if err != nil {
		return nil, err
	}
	var config connection
	if err = json.Unmarshal(bytes.TrimPrefix(data, []byte{239, 187, 191}), &config); err != nil {
		return nil, err
	}
	if config.Host == "" || config.User == "" || strings.HasPrefix(config.Host, "-") || strings.HasPrefix(config.User, "-") {
		return nil, fmt.Errorf("SSH 配置无效")
	}
	if config.Port == 0 {
		config.Port = 22
	}
	if config.Port < 1 || config.Port > 65535 {
		return nil, fmt.Errorf("SSH 端口无效")
	}
	if _, err = os.Stat(config.Key); err != nil {
		return nil, fmt.Errorf("找不到 SSH 密钥；未操作本地数据库")
	}
	input, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "ssh", "-T", "-i", config.Key, "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=2", "-p", strconv.Itoa(config.Port), config.User+"@"+config.Host, `sudo -n bash -c 'export "$(cat /etc/kungfu-go/game.env)"; exec /opt/kungfu-go/kungfu-admin'`)
	hideWindow(command)
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("线上请求超时；写入结果可能尚未收到，请保持原操作重试")
	}
	if err != nil {
		detail := stderr.String()
		if len(detail) > 1500 {
			detail = detail[len(detail)-1500:]
		}
		return nil, fmt.Errorf("线上管理连接失败；未回退本地数据库。\n%s", strings.TrimSpace(detail))
	}
	var response struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err = json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("线上管理返回无效响应，请检查管理程序是否已部署")
	}
	if !response.OK {
		return nil, fmt.Errorf("%s", response.Error)
	}
	return response.Result, nil
}
func template(item Item, quantity, days int) []byte {
	record := make([]byte, 68)
	record[4] = item.Kind
	little.PutUint32(record[5:], item.ID)
	if item.Stackable {
		little.PutUint16(record[23:], uint16(quantity))
	} else if item.Timed {
		little.PutUint32(record[13:], uint32(days*24))
	}
	if item.Kind == protocol.ItemTalisman {
		little.PutUint16(record[23:], initialTalismanQuota)
	}
	return record
}
func offer(item Item, request Request) (persistence.AdminOffer, error) {
	var result persistence.AdminOffer
	if request.Enabled == nil || (request.Currency != "gold" && request.Currency != "ticket") || request.Price < 1 || request.Price > 2147483647 || request.Days < 1 || request.Days > 3650 || request.Quantity < 1 || request.Quantity > 999 || item.ID > 0x8fffffff {
		return result, fmt.Errorf("价格、期限、数量或上架状态无效")
	}
	key := 0x70000000 + item.ID
	record := make([]byte, 108)
	little.PutUint32(record, key)
	record[4] = item.Kind
	little.PutUint32(record[5:], item.ID)
	little.PutUint32(record[9:], key)
	if len(item.Fields) > 6 {
		mask, err := strconv.ParseUint(item.Fields[6], 10, 32)
		if err != nil {
			return result, err
		}
		little.PutUint32(record[14:], uint32(mask))
	}
	offset := 30
	if request.Currency == "ticket" {
		offset = 38
	}
	little.PutUint32(record[offset:], uint32(request.Price))
	little.PutUint32(record[offset+4:], uint32(request.Price))
	record[48] = 1
	record[83] = 1
	if item.Stackable {
		little.PutUint32(record[26:], uint32(request.Quantity))
	} else {
		little.PutUint32(record[22:], uint32(request.Days*24))
	}
	if item.Kind == protocol.ItemTalisman {
		little.PutUint32(record[26:], initialTalismanQuota)
	}
	grant := template(item, request.Quantity, request.Days)
	if item.Kind == protocol.ItemTalisman {
		// Native 99AB34 looks up maximum durability in the shop directory by
		// inventory +9; 99AB48 reads the directory's DWORD +26.
		little.PutUint32(grant[9:], key)
	}
	return persistence.AdminOffer{Offer: persistence.Offer{Key: key, Category: 10, Variant: item.Kind, Record: record, Grant: grant}, Enabled: *request.Enabled, Recommended: request.Recommended, ServerExpiryDays: request.ServerExpiryDays}, nil
}
func (admin *Admin) Call(request Request) (any, error) {
	if strings.HasPrefix(request.Operation, "client_config_") {
		if request.ClientConfig == nil {
			request.ClientConfig = &clientConfigRequest{}
		}
		return admin.clientConfig(request.Operation, *request.ClientConfig)
	}
	if request.Environment != "" && request.Environment != "local" && request.Environment != "online" {
		return nil, fmt.Errorf("无效的管理环境")
	}
	call := admin.Remote
	environment := "线上服务器"
	if request.Environment == "local" {
		call = admin.local
		environment = "本地测试服"
	}

	transport := call
	call = func(r persistence.AdminRequest) (json.RawMessage, error) {
		r.GMVersion = request.GMVersion
		return transport(r)
	}
	if request.Operation == "gm_version" {
		if request.Environment == "local" {
			return gmversion.Info(), nil
		}
		return call(persistence.AdminRequest{Operation: "gm_version"})
	}
	remote := persistence.AdminRequest{Operation: request.Operation, ID: request.ID, UID: request.UID, Mode: request.Mode, Amount: request.Amount, Rewards: request.Rewards, RewardRevision: request.RewardRevision}
	remote.LoginErrors = request.LoginErrors
	remote.Treasure = request.Treasure
	remote.Instance, remote.ExpiresAt = request.Instance, request.ExpiresAt
	remote.UIDs, remote.All = request.UIDs, request.All
	remote.Reason = request.Reason
	remote.BannedWords = request.BannedWords
	remote.StageAccess = request.StageAccess
	remote.StageUnlocks = request.StageUnlocks
	remote.Honour = request.Honour
	remote.Training = request.Training
	remote.WeaponSettings = request.WeaponSettings
	remote.VIPShopSettings = request.VIPShopSettings
	remote.TalismanSettings = request.TalismanSettings
	remote.Definition = request.Definition
	remote.Tasks = request.Tasks
	remote.Titles = request.Titles
	remote.VIPKind = request.VIPKind
	if request.Operation == "user_ban_save" {
		if request.Enabled == nil {
			return nil, fmt.Errorf("请选择封禁或解封操作")
		}
		remote.Enabled = *request.Enabled
	}
	switch request.Operation {
	case "grant_batch_get", "grant_batch_list", "grant_batch_send", "grant_batch_send_many":
		return call(remote)
	case "notice_send", "notice_status":
		result, err := call(remote)
		if err != nil && err.Error() == "request rejected" {
			return nil, fmt.Errorf("%s尚未部署普通通知功能，请切换本地测试服测试，或先部署对应服务器版本；本次请求已拒绝", environment)
		}
		return result, err
	case "users_list", "user_ban_save", "user_ban_history", "banned_words_get", "banned_words_save", "stage_unlocks_get", "stage_unlocks_save":
		return call(remote)
	case "login_errors_get", "login_errors_save", "treasure_get", "treasure_save", "treasure_preview", "tasks_get", "tasks_save", "titles_get", "titles_save":
		return call(remote)
	case "vip_shop_settings_get", "vip_shop_settings_save", "talisman_settings_get", "talisman_settings_save", "weapon_settings_get", "weapon_settings_save", "training_get", "training_save", "stages_get", "stages_save", "honour_get", "honour_save", "vip_get", "vip_grant":
		return call(remote)
	case "definitions_get", "definition_save", "accounts", "inventory", "inventory_expiry", "wallet_accounts", "wallet_update", "rewards_get", "rewards_save":
		return call(remote)
	}
	// Switching which client the tool works on is handled before the client
	// directory is resolved, so a wrong pick can be corrected from the UI.
	if request.Operation == "client_directory_get" || request.Operation == "client_directory_set" {
		return admin.clientDirectory(request)
	}
	if request.Operation == "server_config_hash_set" {
		return admin.setServerConfigHash(request)
	}
	client := filepath.Join(admin.Root, "runtime-local", "client")
	pathConfig := filepath.Join(admin.Root, "runtime-local", "client-path.json")
	if data, readErr := os.ReadFile(pathConfig); readErr == nil {
		var config struct {
			Directory string `json:"client_directory"`
		}
		if err := json.Unmarshal(data, &config); err != nil || strings.TrimSpace(config.Directory) == "" {
			return nil, fmt.Errorf("客户端路径配置无效：%s", pathConfig)
		}
		client = config.Directory
		if !filepath.IsAbs(client) {
			client = filepath.Join(admin.Root, client)
		}
	} else if !os.IsNotExist(readErr) {
		return nil, readErr
	}
	if request.Operation == "task_extended_templates" {
		catalogues, hash, err := readExtendedTaskCatalogues(filepath.Join(client, "Data", "config.spf2"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"source": "client_extended_tasks", "catalogues": catalogues, "client_hash": hash}, nil
	}
	if request.Operation == "task_templates" {
		templates, hash, err := readBaseQuestCatalogue(filepath.Join(client, "Data", "config.spf2"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"source": "client_basequest", "templates": templates, "client_hash": hash}, nil
	}
	if request.Operation == "title_catalog" {
		titles, hash, err := readRoleTitleCatalogue(filepath.Join(client, "Data", "config.spf2"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"source": "client_roletitle", "titles": titles, "client_hash": hash}, nil
	}
	if request.Operation == "stage_requirements" {
		rows, pve, hash, err := ReadStageCatalogue(filepath.Join(client, "Data", "config.spf2"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"source": "client_mapmgr", "client_hash": hash, "maps": rows, "pve_maps": pve}, nil
	}
	if request.Operation == "training_missions" {
		// Client catalogue only: never dispatch to an account database or treat
		// these scenario IDs as the task-panel 60xx configuration keys.
		missions, err := ReadTrainingMissions(filepath.Join(client, "Data", "config.spf2"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"source": "client_titlemission", "missions": missions}, nil
	}
	items, err := catalog(client, request.Operation == "catalog" || request.Operation == "weapon_catalog")
	if err != nil {
		return nil, err
	}
	if request.Operation == "shop_images" {
		return shopImages(client, items, request.Keys)
	}
	if request.Operation == "talisman_client_rules" {
		return clientTalismanRules(client, items)
	}
	if request.Operation == "shop_image_status" {
		pictures, err := shopImages(client, items, request.Keys)
		if err != nil {
			return nil, err
		}
		status := make(map[string]bool, len(request.Keys))
		for _, key := range request.Keys {
			_, status[key] = pictures[key]
		}
		return status, nil
	}
	if strings.HasPrefix(request.Operation, "weapon_") {
		result, err := weaponHandle(request, client, items, filepath.Join(admin.Root, "runtime-local", "weapon-config"))
		if err != nil {
			return nil, err
		}
		if release, ok := result.(weaponRelease); ok {
			return admin.publishWeapon(release)
		}
		return result, nil
	}
	if request.Operation == "definition_from_item" {
		for _, item := range items {
			if item.Key != request.Key {
				continue
			}
			if item.ID > 0x1fffffff {
				return nil, fmt.Errorf("物品编号超出奖励目录范围")
			}
			key := uint32(0x60000000) + item.ID // Independent reward namespace, never a player instance.
			d := persistence.ItemDefinition{Key: key, Record: template(item, 1, 7)}
			if item.Timed {
				d.Days = 7
			}
			raw, e := call(persistence.AdminRequest{Operation: "definitions_get"})
			if e != nil {
				return nil, e
			}
			var existing []persistence.ItemDefinition
			if e = json.Unmarshal(raw, &existing); e != nil {
				return nil, e
			}
			for _, old := range existing {
				if old.Key == key {
					if bytes.Equal(old.Record, d.Record) && old.Days == d.Days {
						return old, nil
					}
					return nil, fmt.Errorf("该物品已有不同奖励规格，请从已有定义中选择")
				}
			}
			return call(persistence.AdminRequest{Operation: "definition_save", Definition: &d})
		}
		return nil, fmt.Errorf("物品不在当前客户端目录")
	}
	if request.Operation == "catalog" {
		categories := map[byte]bool{}
		for _, item := range items {
			categories[item.Kind] = true
		}
		return map[string]any{"items": items, "root": admin.Root, "database": environment, "environment": environment, "categories": len(categories)}, nil
	}
	byKey := map[string]Item{}
	available := []Item{}
	for _, item := range items {
		byKey[item.Key] = item
		if item.Supported {
			available = append(available, item)
		}
	}
	if request.Operation == "shop_catalog" {
		encoded, err := call(remote)
		if err != nil {
			return nil, err
		}
		var rows []persistence.AdminOffer
		if err = json.Unmarshal(encoded, &rows); err != nil {
			return nil, err
		}
		offers := map[string]any{}
		for _, row := range rows {
			if len(row.Record) != 108 || len(row.Grant) != 68 {
				return nil, fmt.Errorf("线上商品记录长度异常")
			}
			key := fmt.Sprintf("%d:%d", row.Grant[4], little.Uint32(row.Grant[5:]))
			price := little.Uint32(row.Record[38:])
			currency := "ticket"
			if price == 0 {
				currency = "gold"
				price = little.Uint32(row.Record[30:])
			}
			days := little.Uint32(row.Grant[13:]) / 24
			if days == 0 {
				days = 365
			}
			quantity := little.Uint16(row.Grant[23:])
			if row.Grant[4] == protocol.ItemTalisman {
				quantity = 1
			} // +23 is durability, not purchase count.
			if quantity == 0 {
				quantity = 1
			}
			offers[key] = map[string]any{"recommendation_priority": row.RecommendationPriority, "recommended": row.Recommended, "server_expiry_days": row.ServerExpiryDays, "currency": currency, "price": price, "days": days, "quantity": quantity, "enabled": row.Enabled}
		}
		return map[string]any{"items": items, "offers": offers, "environment": environment}, nil
	}
	if request.Operation == "grant_batch_create" {
		if len(request.BatchItems) < 1 || len(request.BatchItems) > 100 {
			return nil, fmt.Errorf("请选择1–100种道具")
		}
		for _, row := range request.BatchItems {
			if row.Key == "currency:ticket" {
				if row.Quantity < 1 || row.Quantity > 2147483647 {
					return nil, fmt.Errorf("点券数量须为1–2147483647")
				}
				remote.BatchItems = append(remote.BatchItems, persistence.BatchGrantItem{Key: row.Key, Name: "点券", Quantity: row.Quantity})
				continue
			}
			item, ok := byKey[row.Key]
			if !ok || row.Quantity < 1 || row.Quantity > 999 || row.Days < 1 || row.Days > 3650 {
				return nil, fmt.Errorf("道具、数量或期限无效")
			}
			row.Name = item.Name
			row.Stackable = item.Stackable
			row.Record = template(item, row.Quantity, row.Days)
			remote.BatchItems = append(remote.BatchItems, row)
		}
		return call(remote)
	}
	if request.Operation != "grant" && request.Operation != "shop_save" && request.Operation != "shop_batch" && request.Operation != "shop_prices" && request.Operation != "shop_rank" {
		return nil, fmt.Errorf("不支持的管理操作")
	}
	if len(request.ID) < 1 || len(request.ID) > 100 {
		return nil, fmt.Errorf("操作编号无效")
	}
	keys := request.Keys
	if request.Operation == "shop_save" {
		keys = []string{request.Key}
	}
	if request.All && request.Operation == "shop_batch" {
		keys = []string{}
		for _, item := range available {
			keys = append(keys, item.Key)
		}
	}
	if len(keys) < 1 || len(keys) > 4000 {
		return nil, fmt.Errorf("请选择 1–4000 件道具")
	}
	seen := map[string]bool{}
	for _, key := range keys {
		item, exists := byKey[key]
		delisting := request.Operation == "shop_batch" && request.Enabled != nil && !*request.Enabled
		if !exists || seen[key] || (request.Operation != "grant" && !item.Supported && !delisting) {
			return nil, fmt.Errorf("道具无效或重复")
		}
		seen[key] = true
	}
	if request.Operation == "shop_rank" {
		remote.Keys, remote.RecommendationPriority, remote.PinRecommended = keys, request.RecommendationPriority, request.PinRecommended
		return call(remote)
	}
	if request.Operation == "shop_prices" {
		if request.Price < 1 || request.Price > 2147483647 || (request.Currency != "gold" && request.Currency != "ticket") {
			return nil, fmt.Errorf("售价须为 1–2147483647，币种须为金币或点券")
		}
		remote.Keys, remote.Currency, remote.Price = keys, request.Currency, request.Price
		return call(remote)
	}
	if request.Operation == "grant" {
		if request.Quantity < 1 || request.Quantity > 999 || request.Days < 1 || request.Days > 3650 {
			return nil, fmt.Errorf("数量或期限无效")
		}
		for _, key := range keys {
			remote.Records = append(remote.Records, template(byKey[key], request.Quantity, request.Days))
		}
	} else {
		if request.Enabled == nil {
			return nil, fmt.Errorf("上架状态无效")
		}
		remote.Enabled = *request.Enabled
		remote.All = request.All && request.Operation == "shop_batch"
		remote.Preserve = request.Operation == "shop_batch"
		if remote.Preserve {
			request.Currency = "ticket"
			request.Price = 100
			request.Days = 365
			request.Quantity = 1
		}
		for _, key := range keys {
			row, err := offer(byKey[key], request)
			if err != nil {
				return nil, err
			}
			remote.Offers = append(remote.Offers, row)
		}
	}
	return call(remote)
}
