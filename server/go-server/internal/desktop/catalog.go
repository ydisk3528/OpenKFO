package desktop

import (
	"fmt"
	"kungfu.local/server/internal/protocol"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Item struct {
	Key         string   `json:"key"`
	ID          uint32   `json:"id"`
	Kind        byte     `json:"kind"`
	Name        string   `json:"name"`
	Group       string   `json:"group"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Icon        string   `json:"icon"`
	Gender      string   `json:"gender"`
	Stackable   bool     `json:"stackable"`
	Timed       bool     `json:"timed"`
	Supported   bool     `json:"supported"`
	Fields      []string `json:"fields"`
}

var kinds = map[byte][2]string{
	12: {"服装外观", "上衣"}, 13: {"服装外观", "脸型"}, 14: {"服装外观", "鞋子"}, 15: {"服装外观", "头发 / 帽子"}, 16: {"服装外观", "裤子"}, 17: {"服装外观", "手套"}, 18: {"服装外观", "套装"}, 20: {"服装外观", "头部饰品"}, 21: {"服装外观", "背部饰品"}, 25: {"武器装备", "武器"}, 26: {"武器装备", "投掷 / 副武器"}, 30: {"宠物/法宝", "宠物 / 法宝"}, 31: {"个性装饰", "称号"}, 50: {"材料道具", "许愿瓶"}, 60: {"材料道具", "宝石 / 材料"}, 61: {"材料道具", "转生石"}, 64: {"消耗用品", "战斗药水 / 手雷"}, 68: {"消耗用品", "替身娃娃"}, 71: {"功能卡券", "喇叭卡"}, 72: {"功能卡券", "双倍经验卡"}, 73: {"功能卡券", "VIP / 名侠卡"}, 74: {"功能卡券", "武器切换卡"}, 75: {"功能卡券", "百宝券"}, 76: {"功能卡券", "节日百宝券"}, 77: {"个性装饰", "幻影卡"}, 78: {"功能卡券", "置顶卡"}, 79: {"个性装饰", "个性图标"}, 80: {"功能卡券", "经验 / 改名卡"}, 81: {"礼包活动", "福袋"}, 82: {"礼包活动", "红包"}, 83: {"个性装饰", "特效饰品"}, 84: {"社交婚礼", "婚礼道具"}, 85: {"礼包活动", "爆竹"}, 86: {"礼包活动", "粽子"}, 87: {"礼包活动", "镰刀宝箱"}, 88: {"礼包活动", "破天宝箱"}, 89: {"礼包活动", "龙牙宝箱"}, 90: {"礼包活动", "月饼"}, 91: {"礼包活动", "赤子宝箱"}, 92: {"礼包活动", "烤火鸡"}, 93: {"礼包活动", "圣诞袜"}, 94: {"礼包活动", "五色圣诞袜"}, 95: {"礼包活动", "礼包 / 活动道具"}, 96: {"礼包活动", "新手 / 劳动礼包"}, 99: {"功能卡券", "折扣卡"},
}

func stackable(kind byte) bool {
	return kind == 64 || kind == 71 || kind == 74 || kind == 75 || kind == 76
}

// The weapon exchange card is a quantity-based material, not an equippable
// weapon or a stage admission token. Listing it does not implement redemption.
func quantityItem(kind byte, id uint32) bool {
	return stackable(kind) || stageTicket(kind, id) || (kind == 60 && id == 603302)
}

// Verified item.txt admission tokens, not their crafting fragments. Keep the
// native material type (60); this classifies sale/inventory records only.
func stageTicket(kind byte, id uint32) bool {
	if kind != 60 {
		return false
	}
	switch id {
	case 603316, 603317, 603318: // 街头、庙宇、仓库秘境
		return true
	case 603355: // 古寺僵尸符
		return true
	case 603396, 603397, 603398, 603407: // 密室、巅峰、神罚、挑战BOSS
		return true
	}
	return false
}
func timed(kind byte) bool {
	switch kind {
	case 12, 13, 14, 15, 16, 17, 18, 20, 21, 25, protocol.ItemTalisman,
		protocol.ItemDecorativeTitle, protocol.ItemPhantomCard, protocol.ItemPersonalIcon:
		return true
	}
	return false
}
func Catalog(client string) ([]Item, error) { return catalog(client, true) }

func catalog(client string, icons bool) ([]Item, error) {
	a, err := loadArchive(filepath.Join(client, "Data", "config.spf2"))
	if err != nil {
		return nil, err
	}
	text, err := a.text("item.txt")
	if err != nil {
		return nil, err
	}
	return itemsFromText(client, text, icons)
}

// itemsFromText builds the catalogue for one item.txt body. It is separate from
// catalog so weapon creation can inspect a configuration that already carries
// the new rows but has not been written to disk yet.
func itemsFromText(client, text string, icons bool) ([]Item, error) {
	items := []Item{}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 17 {
			return nil, fmt.Errorf("物品配置字段不完整")
		}
		kind, err := strconv.ParseUint(fields[0], 10, 8)
		if err != nil {
			return nil, err
		}
		id, err := strconv.ParseUint(fields[1], 10, 32)
		if err != nil || id == 0 {
			return nil, fmt.Errorf("物品编号错误")
		}
		key := fmt.Sprintf("%d:%d", kind, id)
		if seen[key] {
			return nil, fmt.Errorf("重复物品 %s", key)
		}
		seen[key] = true
		labels, ok := kinds[byte(kind)]
		if !ok {
			labels = [2]string{"其他道具", fmt.Sprintf("类型 %d", kind)}
		}
		icon := ""
		if icons {
			icon, err = catalogIcon(client, byte(kind), id, fields)
			if err != nil {
				return nil, err
			}
		}
		gender := "通用"
		if strings.Contains(fields[3], "（男）") || strings.Contains(fields[3], "(男)") {
			gender = "男"
		} else if strings.Contains(fields[3], "（女）") || strings.Contains(fields[3], "(女)") {
			gender = "女"
		}
		description := fields[16]
		if description == "#" {
			description = ""
		}
		quantityItem := quantityItem(byte(kind), uint32(id))
		if stageTicket(byte(kind), uint32(id)) {
			labels = [2]string{"闯关道具", "闯关门票"}
		}
		items = append(items, Item{key, uint32(id), byte(kind), fields[3], labels[0], labels[1], description, icon, gender, quantityItem, timed(byte(kind)), quantityItem || timed(byte(kind)), fields})
	}
	return items, nil
}

// catalogIcon resolves the picture shown for one inventory entry. The icon
// column (item.txt field 10) wins whenever it points at a usable file; only then
// does it fall back to textures derived from the model column. The fallback is
// required because thrown sub-weapons and several costume sets ship '#' in the
// icon column while still having a texture the client itself draws.
func catalogIcon(client string, kind byte, id uint64, fields []string) (string, error) {
	iconRoot := filepath.Join(client, "Data", "UI")
	if len(fields) < 10 {
		return "", nil
	}
	relative := strings.ReplaceAll(fields[9], "\\", "/")
	if strings.Contains(relative, ":") || strings.HasPrefix(relative, "/") {
		return "", fmt.Errorf("图标路径越界")
	}
	source := filepath.Join(iconRoot, filepath.FromSlash(relative))
	rel, err := filepath.Rel(iconRoot, source)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("图标路径越界")
	}
	if info, err := os.Stat(source); err == nil && !info.IsDir() {
		resolved, err := filepath.EvalSymlinks(source)
		if err != nil {
			return "", err
		}
		resolvedRoot, err := filepath.EvalSymlinks(iconRoot)
		if err != nil {
			return "", err
		}
		rel, err = filepath.Rel(resolvedRoot, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("图标链接越界")
		}
		// All catalog Image.file callers receive the same recovered PNG as the shop.
		if icon, err := cachedTexture(resolved); err == nil {
			return icon, nil
		}
	}
	for _, candidate := range modelTextures(client, kind, id, fields) {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(filepath.Dir(filepath.Dir(iconRoot)), resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if icon, err := cachedTexture(resolved); err == nil {
			return icon, nil
		}
	}
	return "", nil
}

// itemCategories maps an item kind to the costume part the client bakes into its
// role texture name (cha_<gender>_<set>_<part>.png).
var itemCategories = map[byte]string{12: "body", 13: "face", 14: "foot", 15: "hair", 16: "leg", 17: "hand", 18: "body"}

// modelTextures lists fallback pictures derived from item.txt field 8 (the model
// file), in priority order. Names come from filepath.Base so no value from the
// configuration can escape the texture directory.
func modelTextures(client string, kind byte, id uint64, fields []string) []string {
	if len(fields) < 8 {
		return nil
	}
	stem := textureStem(fields[7])
	if stem == "" {
		return nil
	}
	data := filepath.Join(client, "Data")
	number := strconv.FormatUint(id, 10)
	switch kind {
	case 25, 26: // 武器 / 投掷 / 副武器
		return []string{
			filepath.Join(data, "Weapon", "Texture", stem+".png"),
			filepath.Join(data, "Weapon", "Texture", number+".png"),
		}
	case 12, 13, 14, 15, 16, 17, 18: // 服装外观
		part := itemCategories[kind]
		return []string{
			filepath.Join(data, "Role", "Texture", stem+"_"+part+".png"),
			filepath.Join(data, "Role", "Texture", stem+".png"),
			filepath.Join(data, "Role", "Texture", number+".png"),
		}
	}
	return nil
}

// textureStem lowers a model reference to a bare, path-safe file stem.
func textureStem(model string) string {
	model = filepath.Base(strings.ReplaceAll(model, "\\", "/"))
	if dot := strings.IndexByte(model, '.'); dot >= 0 {
		model = model[:dot]
	}
	model = strings.TrimSpace(model)
	if model == "" || model == "#" || strings.ContainsAny(model, `/\:`) {
		return ""
	}
	return strings.ToLower(model)
}
