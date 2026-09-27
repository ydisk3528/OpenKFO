package game

import (
	"kungfu.local/server/internal/protocol"
	"log"
	"time"
)

const networkProbeTimeout = 10 * time.Second

type roomNetworkProbe struct {
	started time.Time
	pending map[uint64]*Session
	timer   *time.Timer
}

// Native 4150 sets the ready gate: only send after all members are ready.
func (h *Hub) beginNetworkProbe(r *Room) {
	if r.NetworkProbe != nil || r.Stage != "room" || tutorialRoom(r) || len(r.Members) == 0 {
		return
	}
	for _, m := range r.Members {
		if (!m.Spectator && !m.Ready) || m.Session.Room != r || m.Session.game() == nil || m.Session.game().Phase != "room" {
			return
		}
	}
	p := &roomNetworkProbe{started: time.Now(), pending: map[uint64]*Session{}}
	r.NetworkProbe = p
	for uid, m := range r.Members {
		m.NetworkDelay = 0
		if m.Spectator {
			continue
		}
		p.pending[uid] = m.Session
		m.Session.sendGame(protocol.Message{ID: protocol.MsgNetworkDelayProbe})
	}
	p.timer = time.AfterFunc(networkProbeTimeout, func() {
		h.lockState()
		defer h.unlockState()
		h.expireNetworkProbe(r, p)
	})
}

func (h *Hub) cancelNetworkProbe(r *Room) {
	if p := r.NetworkProbe; p != nil {
		if p.timer != nil {
			p.timer.Stop()
		}
		r.NetworkProbe = nil
	}
}

func (h *Hub) expireNetworkProbe(r *Room, p *roomNetworkProbe) {
	if h.Rooms[r.ID] != r || r.NetworkProbe != p {
		return
	}
	for uid, session := range p.pending {
		log.Printf("network_probe_timeout room=%d uid=%d account=%q elapsed_ms=%d", r.ID, uid, session.Account, time.Since(p.started).Milliseconds())
	}
	h.clearRoomReady(r)
	h.broadcast(r, notice("开战前网络检测超时，请重新准备。"), 0)
}

func (h *Hub) networkDelayReply(s *Session, payload []byte) error {
	if len(payload) != 0 {
		return protocol.ErrFrame
	}
	r := s.Room
	if r == nil || r.Stage != "room" || r.NetworkProbe == nil {
		return nil
	}
	p := r.NetworkProbe
	m := r.Members[s.UID]
	if m == nil || m.Session != s || (!m.Spectator && !m.Ready) || p.pending[s.UID] != s {
		return nil
	}
	elapsed := time.Since(p.started)
	if elapsed >= networkProbeTimeout {
		h.expireNetworkProbe(r, p)
		return nil
	}
	// Empty replies have no nonce: they are telemetry, never authorization.
	m.NetworkDelay = uint32((elapsed + time.Millisecond - 1) / time.Millisecond)
	if m.NetworkDelay == 0 {
		m.NetworkDelay = 1
	}
	delete(p.pending, s.UID)
	if len(p.pending) != 0 {
		return nil
	}
	h.cancelNetworkProbe(r)
	for _, peer := range r.Members {
		if (!peer.Spectator && !peer.Ready) || peer.Session.game().Phase != "room" || time.Now().After(peer.Session.P2PUntil) {
			h.clearRoomReady(r)
			return nil
		}
	}
	allows, err := h.stageGate(roomPlayers(r)...)
	if err != nil || !allows(protocol.ReadUint32(r.Request, 38)) {
		h.clearRoomReady(r)
		h.broadcast(r, notice("地图条件已改变，请重新选择地图后准备。"), 0)
		return nil
	}
	if err := h.startBattle(r); err != nil {
		h.clearRoomReady(r)
		if r.Type() == protocol.StageAssault || r.Type() == protocol.FosterMode {
			log.Printf("stage_start_failed room=%d owner=%d error=%v", r.ID, r.Owner, err)
			h.broadcast(r, notice("关卡开战失败："+err.Error()), 0)
			return nil
		}
		h.broadcast(r, notice("开战初始化失败，请稍后重新准备。"), 0)
		return err
	}
	return nil
}
