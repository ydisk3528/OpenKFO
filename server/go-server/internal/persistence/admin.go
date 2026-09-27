package persistence

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kungfu.local/server/internal/protocol"
)

type AdminOffer struct {
	RecommendationPriority *int32 `json:"recommendation_priority,omitempty"`
	Recommended            *bool  `json:"recommended,omitempty"`
	// nil preserves an existing policy for older GM clients; zero is permanent.
	ServerExpiryDays *uint32 `json:"server_expiry_days,omitempty"`
	Offer
	Enabled bool `json:"enabled"`
}
type AdminRequest struct {
	RecommendationPriority int32                `json:"recommendation_priority"`
	PinRecommended         bool                 `json:"pin_recommended"`
	BatchItems             []BatchGrantItem     `json:"batch_items,omitempty"`
	UIDs                   []uint64             `json:"uids,omitempty"`
	Reason                 string               `json:"reason"`
	BannedWords            *BannedWordsSettings `json:"banned_words,omitempty"`
	GMVersion              string               `json:"gm_version"`
	Definition             *ItemDefinition      `json:"definition,omitempty"`
	StageUnlocks           *StagePlayerUnlocks  `json:"stage_unlocks,omitempty"`
	WeaponSettings         *WeaponSettings      `json:"weapon_settings,omitempty"`
	VIPShopSettings        *VIPShopSettings     `json:"vip_shop_settings,omitempty"`
	TalismanSettings       *TalismanSettings    `json:"talisman_settings,omitempty"`
	Titles                 *TitleSettings       `json:"titles,omitempty"`
	Tasks                  *TaskSettings        `json:"tasks,omitempty"`
	Training               *TrainingSettings    `json:"training,omitempty"`
	VIPKind                uint32               `json:"vip_kind,omitempty"`
	Honour                 *HonourSettings      `json:"honour,omitempty"`
	StageAccess            *StageAccess         `json:"stage_access,omitempty"`
	Instance               uint32               `json:"instance"`
	ExpiresAt              *int64               `json:"expires_at,omitempty"`
	Keys                   []string             `json:"keys,omitempty"`
	Currency               string               `json:"currency,omitempty"`
	Price                  int64                `json:"price,omitempty"`
	Rewards                *RewardRules         `json:"rewards,omitempty"`
	RewardRevision         uint64               `json:"reward_revision"`
	Operation              string               `json:"operation"`
	ID                     string               `json:"id"`
	UID                    uint64               `json:"uid"`
	Mode                   string               `json:"mode"`
	Amount                 uint32               `json:"amount"`
	Records                [][]byte             `json:"records"`
	Offers                 []AdminOffer         `json:"offers"`
	Enabled                bool                 `json:"enabled"`
	All                    bool                 `json:"all"`
	Preserve               bool                 `json:"preserve"`
}

func itemKey(record []byte) string {
	return fmt.Sprintf("%d:%d", record[4], protocol.ReadUint32(record, 5))
}
func (store *Store) adminOffers() ([]AdminOffer, error) {
	rows, err := store.DB.Query(`SELECT o.catalog_key,o.category,o.variant,o.record,o.grant_record,o.enabled,COALESCE(l.days,0),COALESCE(f.enabled,FALSE),COALESCE(p.priority,0) FROM offers o LEFT JOIN offer_lifetimes l ON l.catalog_key=o.catalog_key LEFT JOIN offer_recommendations f ON f.catalog_key=o.catalog_key LEFT JOIN offer_recommendation_order p ON p.catalog_key=o.catalog_key ORDER BY o.catalog_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AdminOffer{}
	for rows.Next() {
		var offer AdminOffer
		if err = rows.Scan(&offer.Key, &offer.Category, &offer.Variant, &offer.Record, &offer.Grant, &offer.Enabled, &offer.ServerExpiryDays, &offer.Recommended, &offer.RecommendationPriority); err != nil {
			return nil, err
		}
		result = append(result, offer)
	}
	return result, rows.Err()
}
func (store *Store) Admin(request AdminRequest) (any, error) {
	switch request.Operation {
	case "grant_batch_create", "grant_batch_get", "grant_batch_list", "grant_batch_send", "grant_batch_send_many":
		return store.adminGrantBatch(request)
	case "users_list":
		return store.UsersList()
	case "user_ban_save":
		return store.SaveAccountBan(request)
	case "user_ban_history":
		return store.AccountBanHistory(request.UID)
	case "banned_words_get":
		return store.BannedWords()
	case "banned_words_save":
		if request.BannedWords == nil {
			return nil, ErrDenied
		}
		return store.SaveBannedWords(*request.BannedWords)
	case "definitions_get":
		return store.ItemManager().Definitions()
	case "definition_save":
		if request.Definition == nil {
			return nil, ErrDenied
		}
		return store.ItemManager().SaveDefinition(*request.Definition)
	case "titles_get":
		return store.TitleManager().TitleSettings()
	case "titles_save":
		if request.Titles == nil {
			return nil, ErrDenied
		}
		return store.TitleManager().SaveTitleSettings(*request.Titles)
	case "tasks_get":
		return store.TaskManager().TaskSettings()
	case "tasks_save":
		if request.Tasks == nil {
			return nil, ErrDenied
		}
		return store.TaskManager().SaveTaskSettings(*request.Tasks)
	case "vip_get":
		if request.UID == 0 {
			return nil, ErrDenied
		}
		var uid uint64
		if err := store.DB.QueryRow("SELECT uid FROM accounts WHERE uid=?", request.UID).Scan(&uid); err != nil {
			return nil, err
		}
		return store.VIPMembership(uid)
	case "training_get":
		return store.TrainingManager().TrainingSettings()
	case "weapon_settings_get":
		return store.ItemManager().WeaponSettings()
	case "vip_shop_settings_get":
		return store.ShopManager().VIPShopSettings()
	case "vip_shop_settings_save":
		if request.VIPShopSettings == nil {
			return nil, ErrDenied
		}
		return store.ShopManager().SaveVIPShopSettings(*request.VIPShopSettings)
	case "weapon_settings_save":
		if request.WeaponSettings == nil {
			return nil, ErrDenied
		}
		return store.ItemManager().SaveWeaponSettings(*request.WeaponSettings)
	case "talisman_settings_get":
		return store.ItemManager().TalismanSettings()
	case "talisman_settings_save":
		if request.TalismanSettings == nil {
			return nil, ErrDenied
		}
		return store.ItemManager().SaveTalismanSettings(*request.TalismanSettings)
	case "training_save":
		if request.Training == nil {
			return nil, ErrDenied
		}
		return store.TrainingManager().SaveTrainingSettings(*request.Training)
	case "honour_get":
		settings, err := store.HonourSettings(HonourRules{})
		if err == nil && settings.Revision == 0 {
			return nil, fmt.Errorf("请先启动新版服务器导入原荣誉配置")
		}
		return settings, err
	case "honour_save":
		if request.Honour == nil {
			return nil, ErrDenied
		}
		return store.SaveHonourSettings(*request.Honour)
	case "stages_get":
		return store.StageAccess()
	case "stage_unlocks_get":
		if _, err := store.TitleManager().AccountTitle(request.UID); err != nil {
			return nil, err
		}
		access, err := store.StageAccess()
		if err != nil {
			return nil, err
		}
		return store.StagePlayerUnlocks(request.UID, access.ClientHash)
	case "stage_unlocks_save":
		if request.StageUnlocks == nil || request.UID == 0 || request.StageUnlocks.UID != request.UID {
			return nil, ErrDenied
		}
		return store.SaveStagePlayerUnlocks(*request.StageUnlocks)
	case "stages_save":
		if request.StageAccess == nil {
			return nil, ErrDenied
		}
		return store.SaveStageAccess(*request.StageAccess)
	case "rewards_get":
		return store.RewardManager().BattleRewards(RewardRules{})
	case "rewards_save":
		if request.Rewards == nil || len(request.Rewards.Levels) != 150 {
			return nil, fmt.Errorf("需要完整的 150 级奖励表，请使用新版 GM管理器")
		}
		return store.RewardManager().SaveBattleRewards(request.RewardRevision, *request.Rewards)
	case "accounts", "wallet_accounts":
		rows, err := store.DB.Query(`SELECT a.uid,a.account,a.nickname,a.gold,a.tickets,(SELECT COUNT(*) FROM inventory i WHERE i.uid=a.uid) FROM accounts a ORDER BY a.uid`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		result := []map[string]any{}
		for rows.Next() {
			var uid uint64
			var account, nickname string
			var gold, tickets, count uint32
			if err = rows.Scan(&uid, &account, &nickname, &gold, &tickets, &count); err != nil {
				return nil, err
			}
			result = append(result, map[string]any{"uid": uid, "account": account, "nickname": nickname, "gold": gold, "tickets": tickets, "count": count})
		}
		return result, rows.Err()
	case "inventory":
		account, err := store.RoleManager().Snapshot(request.UID)
		if err != nil {
			return nil, err
		}
		result := []map[string]any{}
		deadlines := map[uint32]int64{}
		rows, err := store.DB.Query(`SELECT instance,expires_at FROM inventory_expirations WHERE uid=?`, request.UID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id uint32
			var deadline int64
			if err = rows.Scan(&id, &deadline); err != nil {
				rows.Close()
				return nil, err
			}
			deadlines[id] = deadline
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, record := range account.Inventory {
			result = append(result, map[string]any{"instance": protocol.ReadUint32(record, 0), "expires_at": deadlines[protocol.ReadUint32(record, 0)], "key": itemKey(record), "slot": protocol.ReadUint16(record, 17), "quantity": protocol.ReadUint16(record, 23), "duration_hours": protocol.ReadUint32(record, 13), "duration_state": protocol.ReadUint32(record, 19)})
		}
		return result, nil
	case "notice_send", "notice_status":
		return store.AdminNotice(request)
	case "shop_catalog":
		return store.adminOffers()
	case "wallet_update":
		before, after, err := store.WalletManager().AdjustTickets(request.UID, request.Mode, request.Amount, request.ID)
		return map[string]any{"before": before, "after": after, "backup": "线上 wallet_operations 审计记录：" + request.ID, "message": fmt.Sprintf("线上点券：%d → %d；重新登录游戏刷新", before, after)}, err
	case "grant", "shop_save", "shop_batch", "shop_prices", "shop_rank", "inventory_expiry", "vip_grant":
	default:
		return nil, ErrDenied
	}
	if len(request.ID) < 1 || len(request.ID) > 100 || len(request.Records) > 4000 || len(request.Offers) > 4000 {
		return nil, ErrDenied
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	// The audit is committed in the same transaction as the mutation. It keeps
	// before-images and the request hash, so retries never duplicate grants.
	if _, err = store.DB.Exec(`CREATE TABLE IF NOT EXISTS desktop_admin_operations(id VARCHAR(100) CHARACTER SET ascii PRIMARY KEY,request_hash BINARY(32) NOT NULL,before_data LONGBLOB NOT NULL,result_data LONGBLOB NOT NULL,created TIMESTAMP DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`); err != nil {
		return nil, err
	}
	if _, err = store.DB.Exec(`INSERT IGNORE INTO counters(name,value) VALUES('desktop_admin',0)`); err != nil {
		return nil, err
	}
	tx, err := store.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var lock uint64
	if err = tx.QueryRow(`SELECT value FROM counters WHERE name='desktop_admin' FOR UPDATE`).Scan(&lock); err != nil {
		return nil, err
	}
	var previousHash, previousResult []byte
	err = tx.QueryRow(`SELECT request_hash,result_data FROM desktop_admin_operations WHERE id=?`, request.ID).Scan(&previousHash, &previousResult)
	if err == nil {
		if !bytes.Equal(previousHash, digest[:]) {
			return nil, ErrDenied
		}
		var result any
		err = json.Unmarshal(previousResult, &result)
		return result, err
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	result := map[string]any{"backup": "线上事务前快照：desktop_admin_operations / " + request.ID}
	var before any
	if request.Operation == "vip_grant" {
		if request.ExpiresAt == nil {
			return nil, ErrDenied
		}
		var instance uint32
		instance, err = grantVIP(tx, request.UID, request.VIPKind, *request.ExpiresAt, time.Now().Unix())
		if err != nil {
			return nil, err
		}
		before = map[string]any{"uid": request.UID, "instance": instance, "existed": false}
		result["uid"], result["instance"], result["vip_kind"], result["expires_at"] = request.UID, instance, request.VIPKind, *request.ExpiresAt
		result["message"] = "会员卡已保存；需要配套新版服务器同步资格与权益。"
	} else if request.Operation == "inventory_expiry" {
		if request.UID == 0 || request.Instance == 0 || request.ExpiresAt == nil {
			return nil, ErrDenied
		}
		deadline := *request.ExpiresAt
		now := time.Now().Unix()
		if deadline != 0 && (deadline <= now || deadline > now+10*366*24*3600) {
			return nil, ErrDenied
		}
		var uid uint64
		if err = tx.QueryRow(`SELECT uid FROM accounts WHERE uid=? FOR UPDATE`, request.UID).Scan(&uid); err != nil {
			return nil, err
		}
		var record []byte
		if err = tx.QueryRow(`SELECT record FROM inventory WHERE uid=? AND instance=?`, uid, request.Instance).Scan(&record); err != nil {
			return nil, ErrDenied
		}
		if !usableItem(record) {
			return nil, fmt.Errorf("已失效物品不能通过修改期限恢复")
		}
		var previous int64
		err = tx.QueryRow(`SELECT expires_at FROM inventory_expirations WHERE uid=? AND instance=?`, uid, request.Instance).Scan(&previous)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if err == nil && previous <= now {
			return nil, fmt.Errorf("物品已到期，不能修改期限")
		}
		before = map[string]any{"uid": uid, "instance": request.Instance, "expires_at": previous}
		if deadline == 0 {
			_, err = tx.Exec(`DELETE FROM inventory_expirations WHERE uid=? AND instance=?`, uid, request.Instance)
		} else {
			_, err = tx.Exec(`INSERT INTO inventory_expirations(uid,instance,expires_at) VALUES(?,?,?) ON DUPLICATE KEY UPDATE expires_at=VALUES(expires_at),processed=FALSE`, uid, request.Instance, deadline)
		}
		if err != nil {
			return nil, err
		}
		result["uid"], result["instance"], result["expires_at"] = uid, request.Instance, deadline
		result["message"] = "服务器期限已保存；到期后自动失效。客户端期限文字暂不随此设置改变。"
	} else if request.Operation == "grant" {
		if len(request.Records) == 0 {
			return nil, ErrDenied
		}
		var uid uint64
		if err = tx.QueryRow(`SELECT uid FROM accounts WHERE uid=? FOR UPDATE`, request.UID).Scan(&uid); err != nil {
			return nil, err
		}
		rows, readErr := tx.Query(`SELECT record FROM inventory WHERE uid=? ORDER BY instance`, uid)
		if readErr != nil {
			return nil, readErr
		}
		inventory := [][]byte{}
		byItem := map[string][]byte{}
		next := uint32(1048576)
		for rows.Next() {
			var record []byte
			if err = rows.Scan(&record); err != nil {
				rows.Close()
				return nil, err
			}
			if len(record) != 68 {
				rows.Close()
				return nil, ErrDenied
			}
			inventory = append(inventory, record)
			byItem[itemKey(record)] = record
			if instance := protocol.ReadUint32(record, 0); instance >= next {
				if instance == ^uint32(0) {
					rows.Close()
					return nil, ErrDenied
				}
				next = instance + 1
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		before = inventory
		added, updated, skipped := 0, 0, 0
		seen := map[string]bool{}
		for _, template := range request.Records {
			if len(template) != 68 || template[4] == 0 || protocol.ReadUint32(template, 5) == 0 || protocol.ReadUint16(template, 17) != 0 {
				return nil, ErrDenied
			}
			key := itemKey(template)
			if seen[key] {
				return nil, ErrDenied
			}
			seen[key] = true
			stackable := template[4] == protocol.ItemConsumable || template[4] == 71 || template[4] == 74
			count := protocol.ReadUint16(template, 23)
			if stackable && (count == 0 || count > 999) {
				return nil, ErrDenied
			}
			record := bytes.Clone(template)
			if existing := byItem[key]; existing != nil {
				if !stackable {
					skipped++
					continue
				}
				record = bytes.Clone(existing)
				quantity := uint32(protocol.ReadUint16(record, 23)) + uint32(count)
				if quantity > 999 {
					return nil, fmt.Errorf("%s 添加后超过 999，整批未写入", key)
				}
				protocol.WriteUint16(record, 23, uint16(quantity))
				updated++
			} else {
				if next == ^uint32(0) {
					return nil, ErrDenied
				}
				protocol.WriteUint32(record, 0, next)
				next++
				added++
			}
			if _, err = tx.Exec(`INSERT INTO inventory(uid,instance,record) VALUES(?,?,?) ON DUPLICATE KEY UPDATE record=VALUES(record)`, uid, protocol.ReadUint32(record, 0), record); err != nil {
				return nil, err
			}
		}
		result["uid"], result["added"], result["updated"], result["skipped"] = uid, added, updated, skipped
	} else {
		rows, readErr := tx.Query(`SELECT o.catalog_key,o.category,o.variant,o.record,o.grant_record,o.enabled,COALESCE(l.days,0),COALESCE(f.enabled,FALSE),COALESCE(p.priority,0) FROM offers o LEFT JOIN offer_lifetimes l ON l.catalog_key=o.catalog_key LEFT JOIN offer_recommendations f ON f.catalog_key=o.catalog_key LEFT JOIN offer_recommendation_order p ON p.catalog_key=o.catalog_key ORDER BY o.catalog_key FOR UPDATE`)
		if readErr != nil {
			return nil, readErr
		}
		existing := []AdminOffer{}
		byItem := map[string][]AdminOffer{}
		byKey := map[uint32]AdminOffer{}
		for rows.Next() {
			var offer AdminOffer
			if err = rows.Scan(&offer.Key, &offer.Category, &offer.Variant, &offer.Record, &offer.Grant, &offer.Enabled, &offer.ServerExpiryDays, &offer.Recommended, &offer.RecommendationPriority); err != nil {
				rows.Close()
				return nil, err
			}
			if len(offer.Record) != 108 || len(offer.Grant) != 68 {
				rows.Close()
				return nil, ErrDenied
			}
			existing = append(existing, offer)
			byItem[itemKey(offer.Grant)] = append(byItem[itemKey(offer.Grant)], offer)
			byKey[offer.Key] = offer
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		before = existing
		changed := 0
		if request.Operation == "shop_rank" {
			if len(request.Keys) != 1 || request.RecommendationPriority < 0 || request.RecommendationPriority > 1000000 {
				return nil, ErrDenied
			}
			matches := byItem[request.Keys[0]]
			if len(matches) == 0 {
				return nil, fmt.Errorf("商品尚未配置销售记录")
			}
			priority := request.RecommendationPriority
			if request.PinRecommended {
				var maximum int64
				if err = tx.QueryRow("SELECT COALESCE(MAX(priority),0) FROM offer_recommendation_order").Scan(&maximum); err != nil {
					return nil, err
				}
				if maximum >= 1000000 {
					return nil, fmt.Errorf("排序值已达上限，请先调低其他商品排序")
				}
				priority = int32(maximum + 1)
			}
			for _, old := range matches {
				if old.Grant[4] != protocol.ItemWeapon && old.Grant[4] != 21 {
					return nil, ErrDenied
				}
				if _, err = tx.Exec("INSERT INTO offer_recommendation_order(catalog_key,priority) VALUES(?,?) ON DUPLICATE KEY UPDATE priority=VALUES(priority)", old.Key, priority); err != nil {
					return nil, err
				}
				if request.PinRecommended {
					if _, err = tx.Exec("INSERT INTO offer_recommendations(catalog_key,enabled) VALUES(?,TRUE) ON DUPLICATE KEY UPDATE enabled=TRUE", old.Key); err != nil {
						return nil, err
					}
				}
				changed++
			}
			result["recommendation_priority"] = priority
			result["recommendation_priority_saved"] = true
			result["message"] = "推荐排序已保存；刷新商城查看（服务器需已部署排序支持）"
		} else if request.Operation == "shop_prices" {
			if len(request.Keys) < 1 || len(request.Keys) > 4000 || request.Price < 1 || request.Price > 2147483647 || (request.Currency != "gold" && request.Currency != "ticket") {
				return nil, ErrDenied
			}
			selected := map[string]bool{}
			ids := []any{}
			skipped := 0
			for _, key := range request.Keys {
				if selected[key] {
					return nil, ErrDenied
				}
				selected[key] = true
				matches := byItem[key]
				if len(matches) == 0 {
					skipped++
				}
				for _, old := range matches {
					ids = append(ids, old.Key)
				}
			}
			if len(ids) > 0 {
				gold, tickets := uint32(0), uint32(0)
				if request.Currency == "gold" {
					gold = uint32(request.Price)
				} else {
					tickets = uint32(request.Price)
				}
				args := []any{protocol.Uint32Bytes(gold), protocol.Uint32Bytes(gold), protocol.Uint32Bytes(tickets), protocol.Uint32Bytes(tickets)}
				args = append(args, ids...)
				placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
				// Patch only the four existing price DWORDs, in one network round trip.
				update, err := tx.Exec("UPDATE offers SET record=INSERT(INSERT(INSERT(INSERT(record,31,4,?),35,4,?),39,4,?),43,4,?) WHERE catalog_key IN ("+placeholders+")", args...)
				if err != nil {
					return nil, err
				}
				count, err := update.RowsAffected()
				if err != nil {
					return nil, err
				}
				changed = int(count)
			}
			result["skipped"] = skipped
			result["matched"] = len(ids)
			result["message"] = fmt.Sprintf("批量改价完成：匹配 %d 条销售记录，更新 %d 条，跳过 %d 件未配置商品；期限、数量及上下架状态保留。请重新登录游戏刷新商城", len(ids), changed, skipped)
		} else if request.All && !request.Enabled {
			update, updateErr := tx.Exec(`UPDATE offers SET enabled=FALSE WHERE enabled=TRUE`)
			if updateErr != nil {
				return nil, updateErr
			}
			count, _ := update.RowsAffected()
			changed = int(count)
		} else {
			if len(request.Offers) == 0 {
				return nil, ErrDenied
			}
			seen := map[string]bool{}
			for _, offer := range request.Offers {
				if err = validateAdminOffer(offer); err != nil {
					return nil, err
				}
				key := itemKey(offer.Grant)
				if seen[key] {
					return nil, ErrDenied
				}
				seen[key] = true
				matches := byItem[key]
				if request.Preserve && len(matches) > 0 {
					for _, old := range matches {
						if old.Enabled != request.Enabled {
							if _, err = tx.Exec(`UPDATE offers SET enabled=? WHERE catalog_key=?`, request.Enabled, old.Key); err != nil {
								return nil, err
							}
							changed++
						}
					}
					continue
				}
				if request.Preserve && !request.Enabled {
					continue
				}
				if len(matches) > 1 {
					return nil, fmt.Errorf("%s 有多条销售规格，请先单独处理", key)
				}
				if len(matches) == 1 {
					offer.Key = matches[0].Key
					offer.Category = matches[0].Category
					offer.Variant = matches[0].Variant
					protocol.WriteUint32(offer.Record, 0, offer.Key)
					protocol.WriteUint32(offer.Record, 9, offer.Key)
				} else if conflict, ok := byKey[offer.Key]; ok && itemKey(conflict.Grant) != key {
					return nil, fmt.Errorf("商品编号冲突：%s", key)
				}
				if _, err = tx.Exec(`INSERT INTO offers(catalog_key,category,variant,record,grant_record,enabled) VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE record=VALUES(record),grant_record=VALUES(grant_record),enabled=VALUES(enabled)`, offer.Key, offer.Category, offer.Variant, offer.Record, offer.Grant, offer.Enabled); err != nil {
					return nil, err
				}
				if offer.Recommended != nil {
					if _, err = tx.Exec(`INSERT INTO offer_recommendations(catalog_key,enabled) VALUES(?,?) ON DUPLICATE KEY UPDATE enabled=VALUES(enabled)`, offer.Key, *offer.Recommended); err != nil {
						return nil, err
					}
					result["recommendation_saved"] = true
				}
				if offer.ServerExpiryDays != nil {
					if _, err = tx.Exec(`INSERT INTO offer_lifetimes(catalog_key,days) VALUES(?,?) ON DUPLICATE KEY UPDATE days=VALUES(days)`, offer.Key, *offer.ServerExpiryDays); err != nil {
						return nil, err
					}
					result["expiry_policy_saved"] = true
				}
				if _, err = tx.Exec(`INSERT IGNORE INTO item_definitions(definition_key,revision,record,days) SELECT o.catalog_key,1,o.grant_record,COALESCE(l.days,0) FROM offers o LEFT JOIN offer_lifetimes l ON l.catalog_key=o.catalog_key WHERE o.catalog_key=?`, offer.Key); err != nil {
					return nil, err
				}
				changed++
			}
		}
		result["changed"] = changed
		if request.Operation != "shop_prices" && request.Operation != "shop_rank" {
			result["message"] = fmt.Sprintf("商城已更新 %d 条；请重新登录游戏刷新商品缓存", changed)
		}
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return nil, err
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO desktop_admin_operations(id,request_hash,before_data,result_data) VALUES(?,?,?,?)`, request.ID, digest[:], beforeJSON, resultJSON); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func validateAdminOffer(offer AdminOffer) error {
	if offer.ServerExpiryDays != nil && *offer.ServerExpiryDays > 3650 {
		return ErrDenied
	}
	if len(offer.Record) != 108 || len(offer.Grant) != 68 || offer.Key == 0 || offer.Category != 10 || offer.Variant != offer.Grant[4] || offer.Record[4] != offer.Grant[4] || protocol.ReadUint32(offer.Record, 5) != protocol.ReadUint32(offer.Grant, 5) || protocol.ReadUint32(offer.Record, 9) != offer.Key {
		return ErrDenied
	}
	if offer.Recommended != nil && *offer.Recommended && offer.Grant[4] != protocol.ItemWeapon && offer.Grant[4] != 21 {
		return ErrDenied
	}
	gold, tickets := protocol.ReadUint32(offer.Record, 30), protocol.ReadUint32(offer.Record, 38)
	if (gold == 0) == (tickets == 0) || gold > 2147483647 || tickets > 2147483647 || offer.Record[83] != 1 {
		return ErrDenied
	}
	return nil
}
