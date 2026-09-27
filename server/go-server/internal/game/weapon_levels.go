package game

import (
	"bytes"
	"fmt"
	"kungfu.local/server/internal/persistence"

	"kungfu.local/server/internal/protocol"
)

// WeaponLevel describes one native 21411 record. Unknown byte 16 stays explicit;
// it does not imply protection, downgrade, or any server-side success rule.
type WeaponLevel = persistence.WeaponLevel

func (c Config) ValidateWeaponLevels() error {
	if c.WeaponUpgradeMode != "" && c.WeaponUpgradeMode != "consume_score_keep_level" {
		return fmt.Errorf("unsupported weapon_upgrade_mode")
	}
	return (persistence.WeaponSettings{Rules: persistence.WeaponRules{
		Enabled: c.WeaponUpgradeMode != "", Levels: c.WeaponLevels,
	}}).Validate()
}

func (h *Hub) upgradeWeapon(s *Session, ch *Channel, payload []byte) error {
	if len(payload) != 4 {
		return protocol.ErrFrame
	}
	config, revision, err := h.weaponConfig()
	if err != nil {
		return err
	}
	if config.WeaponUpgradeMode == "" {
		s.sendGame(notice("武器升级操作尚未开放。"))
		return nil
	}
	if err := config.ValidateWeaponLevels(); err != nil {
		return err
	}
	rules := make([]persistence.WeaponUpgradeRule, len(config.WeaponLevels))
	for i, row := range config.WeaponLevels {
		rules[i] = persistence.WeaponUpgradeRule{Score: row.ScoreThreshold, Gold: row.Gold, Odds: row.DisplayOdds}
	}
	operation := fmt.Sprintf("%s:%d:%d", s.Namespace, ch.ID, ch.Sequence)
	var result persistence.WeaponUpgradeResult
	if revision != 0 {
		if s.WeaponRevision == 0 {
			s.sendGame(notice("请先重新打开武器升级面板读取配置。"))
			return nil
		}
		result, err = storage2_4(h, h.Store.ItemManager().UpgradeWeaponConfigured, s.UID, operation, protocol.ReadUint32(payload, 0), s.WeaponRevision)
	} else {
		result, err = storage2_4(h, h.Store.ItemManager().UpgradeWeapon, s.UID, operation, protocol.ReadUint32(payload, 0), rules)
	}
	if err != nil {
		s.sendGame(notice("升级未执行，请刷新升级配置，并检查武器归属、期限、等级、熟练度和金币。"))
		return nil
	}
	s.sendGame(protocol.Message{ID: 1240, Payload: protocol.Uint32Bytes(result.Gold)})
	id := uint32(2161)
	instance := protocol.ReadUint32(result.Item, 0)
	if _, exists := s.Inventory[instance]; !exists {
		id = 2160
	}
	s.sendGame(protocol.Message{ID: id, Payload: bytes.Clone(result.Item)})
	if s.Inventory == nil {
		s.Inventory = map[uint32][]byte{}
	}
	s.Inventory[instance] = bytes.Clone(result.Item)
	// Current 861190 consumes only +0 and +9. Other bytes remain reserved;
	// 21413 never substitutes for the preceding inventory update.
	ack := make([]byte, 22)
	if result.Success {
		ack[0] = 1
	}
	protocol.WriteUint32(ack, 9, instance)
	s.sendGame(protocol.Message{ID: 21413, Payload: ack})
	if result.Success && protocol.ReadUint16(result.Item, 17) != 0 {
		h.equipmentChanged(s)
	}
	return nil
}

func (h *Hub) weaponConfig() (Config, uint64, error) {
	c := h.Config
	if h.Store == nil {
		return c, 0, c.ValidateWeaponLevels()
	}
	a, err := storage2_0(h, h.Store.ItemManager().WeaponSettings)
	if err != nil {
		return c, 0, err
	}
	if a.Revision != 0 {
		c.WeaponLevels = a.Rules.Levels
		c.WeaponUpgradeMode = ""
		if a.Rules.Enabled {
			c.WeaponUpgradeMode = "consume_score_keep_level"
		}
	}
	return c, a.Revision, c.ValidateWeaponLevels()
}

func (c Config) weaponLevelPayload() ([]byte, error) {
	if err := c.ValidateWeaponLevels(); err != nil {
		return nil, err
	}
	data := make([]byte, 21*len(c.WeaponLevels))
	for i, row := range c.WeaponLevels {
		record := data[i*21 : (i+1)*21]
		protocol.WriteUint32(record, 0, row.Level)
		protocol.WriteUint32(record, 4, row.ScoreThreshold)
		protocol.WriteUint32(record, 8, row.Gold)
		protocol.WriteUint32(record, 12, row.DisplayOdds)
		record[16] = row.Unknown16
		protocol.WriteUint32(record, 17, row.AttackBonusRaw)
	}
	return data, nil
}
