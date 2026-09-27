package game

import (
	"database/sql"

	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

type HonourRules persistence.HonourRules

func (c Config) ValidateHonour() error { return persistence.HonourRules(c.Honour).Validate() }
func (c Config) honourAward(mode byte, outcome string, players int) (uint32, uint32) {
	return persistence.HonourRules(c.Honour).Award(mode, outcome, players)
}
func (h HonourRules) level(points uint32) uint32 { return persistence.HonourRules(h).Level(points) }

func (h *Hub) honourRules() (persistence.HonourRules, error) {
	rules := persistence.HonourRules(h.Config.Honour)
	if h.Store == nil {
		return rules, rules.Validate()
	}
	s, err := storage2_1(h, h.Store.HonourSettings, rules)
	return s.Rules, err
}
func (h *Hub) honourProfile(s *Session, p []byte) error {
	if len(p) != 12 {
		return protocol.ErrFrame
	}
	period := protocol.ReadUint32(p, 8)
	rules, err := h.honourRules()
	if err != nil {
		return err
	}
	empty := func() { s.sendGame(protocol.Message{ID: 20370, Payload: make([]byte, 37)}) }
	if period == 0 || uint64(period) > uint64(len(rules.Periods)) {
		empty()
		return nil
	}
	r, err := storage2_2(h, h.Store.Honour, protocol.ReadUint64(p, 0), period)
	if err == sql.ErrNoRows {
		empty()
		return nil
	}
	if err != nil {
		return err
	}
	payload := r.Payload(rules.Periods[period-1])
	protocol.WriteUint32(payload, 24, rules.Level(r.Points))
	s.sendGame(protocol.Message{ID: 20370, Payload: payload})
	return nil
}
