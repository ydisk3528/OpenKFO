package game

import (
	"bytes"
	"kungfu.local/server/internal/protocol"
	"slices"
)

func (h *Hub) announceTitleReward(s *Session) error {
	if sent, err := h.announceTutorialReward(s); sent || err != nil {
		return err
	}
	supported := h.Config.TitleLevels
	if h.Config.ConfigHash != "" {
		settings, err := storage2_0(h, h.Store.TitleManager().TitleSettings)
		if err != nil {
			return err
		}
		if settings.Rules.ClientHash != "" {
			supported = settings.Rules.SupportedLevels(h.Config.ConfigHash)
		}
	}
	if len(supported) == 0 {
		return nil
	}
	if s.TitleOffer == 0 {
		advanced, err := storage2_3(h, h.Store.TitleManager().AdvanceTitle, s.UID, supported, h.Config.ConfigHash)
		if err != nil {
			return err
		}
		if advanced {
			level, choices, err := storage3_1(h, h.Store.TitleManager().PendingTitleReward, s.UID)
			if err != nil {
				return err
			}
			if level == 0 && len(choices) == 0 {
				account, err := storage2_1(h, h.Store.RoleManager().Snapshot, s.UID)
				if err != nil {
					return err
				}
				syncTutorialTitle(s, account.Profile[123])
			}
		}
	}
	level, choices, err := storage3_1(h, h.Store.TitleManager().PendingTitleReward, s.UID)
	if err != nil {
		return err
	}
	if level == 0 || !slices.Contains(supported, level) {
		return nil
	}
	// Resend the same pending offer after response loss. Never replace an
	// already bound title with a new one: old 4126 requests have no title ID.
	if s.TitleOffer != 0 && s.TitleOffer != level {
		return nil
	}
	catalog, err := storage2_1(h, h.Store.RewardManager().WeaponChoiceCatalog, choices)
	if err != nil {
		return err
	}
	return sendWeaponReward(s, level, choices, catalog)
}

// Both tutorial graduation and later title rewards use the same native selector.
// Validate the first matching catalogue row, as the client ignores duplicates.
func sendWeaponReward(s *Session, level byte, choices []uint32, catalog []byte) error {
	p, err := protocol.EncodeTitleAward(level, choices)
	if err != nil {
		return err
	}
	const catalogueRecordSize = 108
	if len(catalog)%catalogueRecordSize != 0 {
		return protocol.ErrFrame
	}
	for _, key := range choices {
		found := false
		for offset := 0; offset < len(catalog); offset += catalogueRecordSize {
			row := catalog[offset : offset+catalogueRecordSize]
			if protocol.ReadUint32(row, 9) != key {
				continue
			}
			found = row[4] == protocol.ItemWeapon && protocol.ReadUint32(row, 5) != 0
			break
		}
		if !found {
			return protocol.ErrFrame
		}
	}
	// The native selector resolves catalogue keys before loading item icons.
	// Reward definitions may never have appeared in the purchasable shop list.
	s.sendGame(protocol.Message{ID: 1550, Payload: catalog})
	s.TitleOffer = level
	s.sendGame(protocol.Message{ID: protocol.MsgTitleAward, Payload: p})
	return nil
}

func (h *Hub) claimTitleReward(s *Session, payload []byte) error {
	key, err := protocol.ParseTitleRewardClaim(payload)
	if err != nil || key == 0 || s.TitleOffer == 0 {
		s.sendGame(notice("称号领奖未完成，请先取得并打开本人的奖励资格。"))
		return nil
	}
	item, err := storage2_3(h, h.Store.TitleManager().ClaimTitleReward, s.UID, s.TitleOffer, key)
	if err != nil {
		s.sendGame(notice("称号领奖未完成，请核对奖励资格及所选商品。"))
		return nil
	}
	// Do not clear TitleOffer or replace it with the next pending title: 4126
	// carries no title ID, so a delayed retry could otherwise claim a new award.
	if len(item) != 0 {
		instance := protocol.ReadUint32(item, 0)
		id := uint32(2160)
		if _, exists := s.Inventory[instance]; exists {
			id = 2161
		}
		s.sendGame(protocol.Message{ID: id, Payload: bytes.Clone(item)})
		if s.Inventory == nil {
			s.Inventory = map[uint32][]byte{}
		}
		s.Inventory[instance] = bytes.Clone(item)
	}
	// Native 957280 closes its selection panel after sending 4126. 4127
	// opens another selector and must not be invented as a success ACK.
	s.sendGame(notice("称号奖励已领取，请在背包中查看。"))
	return nil
}
