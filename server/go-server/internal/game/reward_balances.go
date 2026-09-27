package game

import (
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

func (h *Hub) rewardBalances(s *Session, gifts []persistence.LevelGift) error {
	hasCurrency := false
	for _, g := range gifts {
		if g.Gold != 0 || g.Tickets != 0 {
			hasCurrency = true
			break
		}
	}
	if !hasCurrency {
		return nil
	}
	gold, tickets, err := storage3_1(h, h.Store.WalletManager().Balances, s.UID)
	if err != nil {
		return err
	}
	s.sendGame(protocol.Message{ID: 1240, Payload: protocol.Uint32Bytes(gold)})
	s.sendGame(protocol.Message{ID: 1230, Payload: protocol.Uint32Bytes(tickets)})
	return nil
}
