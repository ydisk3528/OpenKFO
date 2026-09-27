package game

import (
	"errors"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"log"
	"time"
)

const weaponSwitchRequestInterval = time.Second

type weaponSwitchAttempt struct {
	serial uint32
	at     time.Time
}

// 4082 has no body or request ID. Rate-limit rapid repeats, but never replay a
// successful toggle: that could give the next animation a free weapon switch.
func (h *Hub) switchWeapon(s *Session, ch *Channel, p []byte) error {
	if len(p) != 0 {
		return protocol.ErrFrame
	}
	r := s.Room
	if r == nil || r.Stage != "battle" || ch.Phase != "battle" {
		return nil
	}
	if r.isObserver(s) {
		return nil
	}
	m := r.Members[s.UID]
	if m == nil || m.Session != s {
		return protocol.ErrFrame
	}
	reply := protocol.Message{ID: protocol.MsgWeaponSwitchResult, Payload: make([]byte, 20)}
	protocol.WriteUint64(reply.Payload, 0, s.UID)
	now := time.Now()
	if m.WeaponSwitch.serial == r.Serial && now.Sub(m.WeaponSwitch.at) < weaponSwitchRequestInterval {
		s.sendGame(reply)
		return nil
	}
	m.WeaponSwitch = weaponSwitchAttempt{r.Serial, now}
	remaining, err := storage2_1(h, h.Store.InventoryManager().ConsumeWeaponSwitchCard, s.UID)
	// Measure the guard from completion; a slow transaction must not exhaust it.
	m.WeaponSwitch.at = time.Now()
	if err != nil {
		// Even rejection must release CSwitchWeaponAct's waiting gate.
		s.sendGame(reply)
		if !errors.Is(err, persistence.ErrDenied) {
			log.Printf("weapon_switch_failed uid=%d room=%d error=%v", s.UID, r.ID, err)
		}
		return nil
	}
	protocol.WriteUint32(reply.Payload, 12, 1)
	protocol.WriteUint32(reply.Payload, 16, remaining)
	// Native 8284F0 resolves UID, releases action+50, applies success+54,
	// and updates the requester's type-74 count via 9CD8E0.
	h.broadcast(r, reply, 0)
	log.Printf("weapon_switch_applied uid=%d room=%d battle=%d remaining=%d", s.UID, r.ID, r.Serial, remaining)
	return nil
}
