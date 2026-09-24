package game

import (
	"log"
	"math"
	"net"
	"net/http"
	"sort"
	"strconv"
	"time"

	"kungfu.local/server/internal/protocol"
)

// Diagnostic EXE handshake, never a native PVE spawn packet. Every fighter must
// create the same fixed NPC locally before its controller can relay actions.
const neutralNPCReady uint32 = 21900
const neutralNPCRequest uint32 = 21901
const neutralNPCIdentity uint64 = 100

// Read-only local probe for the EXE. No database/admin capability is exposed.
func (h *Hub) neutralNPCStatus(w http.ResponseWriter, req *http.Request) {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() || !h.Config.ExperimentalNeutralNPC {
		http.Error(w, "local experiment disabled", http.StatusForbidden)
		return
	}
	id, _ := strconv.ParseUint(req.URL.Query().Get("room"), 10, 16)
	serial, _ := strconv.ParseUint(req.URL.Query().Get("serial"), 10, 32)
	h.Mutex.Lock()
	defer h.Mutex.Unlock()
	r := h.Rooms[uint16(id)]
	if r == nil || r.Serial != uint32(serial) || r.Stage != "battle" || !r.Type().IsTeam() {
		http.Error(w, "team battle not active", http.StatusConflict)
		return
	}
	r.refreshNeutralNPC(time.Now())
	if req.URL.Path == "/experimental/neutral-npc-plan" {
		// Fixed versioned layout, eight player slots. No client memory addresses.
		p := make([]byte, 192)
		protocol.WriteUint32(p, 0, 0x3343504e) // NPC3: monster count/template/AI
		protocol.WriteUint32(p, 4, r.Serial)
		protocol.WriteUint64(p, 8, r.Owner)
		protocol.WriteUint32(p, 20, uint32(r.Type()))
		protocol.WriteUint32(p, 24, uint32(neutralNPCIdentity))
		protocol.WriteUint32(p, 48, uint32(r.ID))
		if n := r.NeutralNPC; n != nil {
			protocol.WriteUint64(p, 8, n.owner)
			protocol.WriteUint32(p, 16, n.state())
			protocol.WriteUint32(p, 28, n.floor)
			protocol.WriteUint32(p, 52, n.monsters)
			protocol.WriteUint32(p, 56, n.template)
			protocol.WriteUint32(p, 60, n.ai)
			for i, v := range n.position {
				protocol.WriteUint32(p, 32+i*4, v)
			}
			var uids []uint64
			for uid := range n.ready {
				uids = append(uids, uid)
			}
			sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
			protocol.WriteUint32(p, 44, uint32(len(uids)))
			for i, uid := range uids {
				status := uint32(1)
				if n.ready[uid] {
					status = 2
				}
				if n.failed[uid] != 0 {
					status = 3
				}
				protocol.WriteUint64(p, 64+i*16, uid)
				protocol.WriteUint32(p, 72+i*16, status)
				protocol.WriteUint32(p, 76+i*16, n.failed[uid])
			}
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(p)
		return
	}
	p := make([]byte, 24)
	protocol.WriteUint32(p, 0, 0x3143504e) // NPC1
	protocol.WriteUint32(p, 4, r.Serial)
	protocol.WriteUint64(p, 8, r.Owner)
	if r.hasNeutralNPC(neutralNPCIdentity) {
		protocol.WriteUint32(p, 16, 1)
	}
	protocol.WriteUint32(p, 20, uint32(r.Type()))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(p)
}

type neutralNPCSession struct {
	serial    uint32
	owner     uint64
	ready     map[uint64]bool
	active    bool
	failed    map[uint64]uint32
	cancelled bool
	deadline  time.Time
	floor     uint32
	position  [3]uint32
	monsters  uint32
	template  uint32
	ai        uint32
}

func (n *neutralNPCSession) state() uint32 {
	if n.cancelled {
		return 3
	}
	if n.active {
		return 2
	}
	return 1
}

func (r *Room) refreshNeutralNPC(now time.Time) {
	n := r.NeutralNPC
	if n == nil || n.cancelled {
		return
	}
	if n.owner != r.Owner || n.serial != r.Serial || r.Stage != "battle" {
		n.cancelled = true
		n.active = false
		return
	}
	for uid := range n.ready {
		m := r.Members[uid]
		if m == nil || m.Spectator {
			if n.active && uid != n.owner {
				delete(n.ready, uid)
				continue
			}
			n.failed[uid] = 5
			n.cancelled = true
		}
	}
	for uid, m := range r.Members {
		if !m.Spectator {
			if _, ok := n.ready[uid]; !ok {
				n.cancelled = true
			}
		}
	}
	if !n.active && !now.Before(n.deadline) {
		for uid, ready := range n.ready {
			if !ready {
				n.failed[uid] = 4
			}
		}
		n.cancelled = true
	}
	if n.cancelled {
		n.active = false
	}
}

// A spawn plan can only originate from the authenticated room owner's battle
// connection. Duplicate requests never restart or move an existing plan.
func (h *Hub) neutralNPCRequest(s *Session, msg protocol.Message) error {
	r, p := s.Room, msg.Payload
	if !h.Config.ExperimentalNeutralNPC {
		return nil
	}
	if r == nil || r.Stage != "battle" || !r.Type().IsTeam() || s.UID != r.Owner || (len(p) != 67 && len(p) != 79) ||
		protocol.ReadUint64(p, 4) != s.UID || p[12] != 1 || p[13] != 1 ||
		protocol.ReadUint32(p, 39) != 100 || protocol.ReadUint32(p, 43) != uint32(r.ID) || protocol.ReadUint32(p, 47) != r.Serial {
		return protocol.ErrFrame
	}
	if m := r.Members[s.UID]; m == nil || m.Session != s || m.Spectator {
		return protocol.ErrFrame
	}
	if r.NeutralNPC != nil {
		return nil
	}
	n := &neutralNPCSession{serial: r.Serial, owner: r.Owner, ready: map[uint64]bool{}, failed: map[uint64]uint32{}, deadline: time.Now().Add(45 * time.Second), floor: protocol.ReadUint32(p, 51)}
	n.monsters = 1
	if len(p) == 79 {
		n.monsters = protocol.ReadUint32(p, 67)
		n.template = protocol.ReadUint32(p, 71)
		n.ai = protocol.ReadUint32(p, 75)
		if n.monsters < 1 || n.monsters > 4 || n.template > 2 || n.ai > 1 {
			return protocol.ErrFrame
		}
	}
	for i := range n.position {
		v := protocol.ReadUint32(p, 55+i*4)
		f := float64(math.Float32frombits(v))
		if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > 100000 {
			return protocol.ErrFrame
		}
		n.position[i] = v
	}
	for uid, m := range r.Members {
		if !m.Spectator {
			if uid >= 100 && uid < 100+uint64(n.monsters) {
				return protocol.ErrFrame
			}
			n.ready[uid] = false
		}
	}
	if len(n.ready) == 0 || len(n.ready) > 8 {
		return protocol.ErrFrame
	}
	r.NeutralNPC = n
	log.Printf("neutral_npc_requested room=%d serial=%d owner=%d clients=%d monsters=%d template=%d ai=%d", r.ID, r.Serial, r.Owner, len(n.ready), n.monsters, n.template, n.ai)
	return nil
}

// Local experiment coordinator reports worker failures only; it cannot request
// a spawn or mark a player ready. Ready receipts still use authenticated battle.
func (h *Hub) neutralNPCFailure(w http.ResponseWriter, req *http.Request) {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() || !h.Config.ExperimentalNeutralNPC {
		http.Error(w, "local only", 403)
		return
	}
	id, _ := strconv.ParseUint(req.URL.Query().Get("room"), 10, 16)
	serial, _ := strconv.ParseUint(req.URL.Query().Get("serial"), 10, 32)
	uid, _ := strconv.ParseUint(req.URL.Query().Get("uid"), 10, 64)
	code, _ := strconv.ParseUint(req.URL.Query().Get("code"), 10, 32)
	if code < 1 || code > 3 {
		http.Error(w, "invalid reason", 400)
		return
	}
	h.Mutex.Lock()
	defer h.Mutex.Unlock()
	r := h.Rooms[uint16(id)]
	if r == nil || r.Serial != uint32(serial) || r.Stage != "battle" || r.NeutralNPC == nil {
		http.Error(w, "stale plan", 409)
		return
	}
	r.refreshNeutralNPC(time.Now())
	n := r.NeutralNPC
	if _, ok := n.ready[uid]; !ok {
		http.Error(w, "not a participant", 403)
		return
	}
	if !n.active && !n.cancelled {
		n.failed[uid] = uint32(code)
		n.cancelled = true
		log.Printf("neutral_npc_failed room=%d serial=%d uid=%d code=%d", r.ID, r.Serial, uid, code)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Room) hasNeutralNPC(uid uint64) bool {
	r.refreshNeutralNPC(time.Now())
	n := r.NeutralNPC
	return r.Type().IsTeam() && r.Stage == "battle" &&
		n != nil && n.active && n.serial == r.Serial && n.owner == r.Owner && uid >= neutralNPCIdentity && uid < neutralNPCIdentity+uint64(n.monsters)
}

func (h *Hub) neutralNPCReady(s *Session, msg protocol.Message) error {
	r, p := s.Room, msg.Payload
	if !h.Config.ExperimentalNeutralNPC || r == nil || !r.Type().IsTeam() {
		return nil
	}
	if (len(p) != 51 && len(p) != 63) || protocol.ReadUint64(p, 4) != s.UID ||
		protocol.ReadUint32(p, 39) != uint32(neutralNPCIdentity) ||
		protocol.ReadUint32(p, 43) != uint32(r.ID) || protocol.ReadUint32(p, 47) != r.Serial ||
		p[12] != 1 || p[13] != 1 {
		return protocol.ErrFrame
	}
	if r.Members[neutralNPCIdentity] != nil {
		return protocol.ErrFrame
	}
	r.refreshNeutralNPC(time.Now())
	n := r.NeutralNPC
	if n == nil || n.serial != r.Serial || n.cancelled {
		return protocol.ErrFrame
	}
	if len(p) == 63 {
		if protocol.ReadUint32(p, 51) != n.monsters || protocol.ReadUint32(p, 55) != n.template || protocol.ReadUint32(p, 59) != n.ai {
			return protocol.ErrFrame
		}
	} else if n.monsters != 1 || n.template != 0 || n.ai != 0 {
		return protocol.ErrFrame
	}
	if m := r.Members[s.UID]; m == nil || m.Session != s || m.Spectator {
		return protocol.ErrFrame
	}
	if _, ok := n.ready[s.UID]; !ok {
		return protocol.ErrFrame
	}
	if n.owner != r.Owner || n.active || n.ready[s.UID] {
		return nil
	}
	n.ready[s.UID] = true
	for uid, m := range r.Members {
		if !m.Spectator && !n.ready[uid] {
			log.Printf("neutral_npc_wait room=%d serial=%d ready_uid=%d", r.ID, r.Serial, s.UID)
			return nil
		}
	}
	n.active = true
	log.Printf("neutral_npc_ready room=%d serial=%d owner=%d actor=%d", r.ID, r.Serial, r.Owner, neutralNPCIdentity)
	return nil
}
