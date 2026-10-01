package game

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"kungfu.local/server/internal/protocol"
)

func TestNeutralNPCRandomPlanIsFixedForReplicas(t *testing.T) {
	for i := 0; i < 32; i++ {
		h, owner, _, _ := combatFixture()
		h.Config.ExperimentalNeutralNPC = true
		r := owner.Room
		r.Request[46] = byte(protocol.TeamSurvival)
		m := extendedPacket(neutralNPCRequest, 79, owner.UID, 100, 43)
		protocol.WriteUint32(m.Payload, 71, 3)
		if err := h.battleMessage(owner, owner.game(), m); err != nil {
			t.Fatal(err)
		}
		n := r.NeutralNPC
		if n == nil || n.monsters < 1 || n.monsters > 4 || (n.template != 0 && n.template != 2) {
			t.Fatal("invalid resolved random plan")
		}
		if err := h.battleMessage(owner, owner.game(), m); err != nil {
			t.Fatal(err)
		}
		if r.NeutralNPC != n {
			t.Fatal("duplicate request rerolled plan")
		}
		p := r.neutralNPCPlan()
		if protocol.ReadUint32(p, 52) != n.monsters || protocol.ReadUint32(p, 56) != n.template {
			t.Fatal("replica plan differs")
		}
	}
}

func requestNeutralNPC(t *testing.T, h *Hub, owner *Session) protocol.Message {
	t.Helper()
	m := extendedPacket(neutralNPCRequest, 67, owner.UID, 100, 43)
	protocol.WriteUint32(m.Payload, 55, 0x41200000) // x = 10, fixed for every replica
	if err := h.battleMessage(owner, owner.game(), m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNeutralNPCRequiresEveryFighterAndController(t *testing.T) {
	h, owner, peer, outsider := combatFixture()
	r := owner.Room
	r.Request[46] = byte(protocol.TeamSurvival)
	ready := func(s *Session) protocol.Message {
		m := extendedPacket(neutralNPCReady, 51, s.UID, 100, 43)
		return m
	}
	if err := h.battleMessage(owner, owner.game(), ready(owner)); err != nil {
		t.Fatal(err)
	}
	if r.NeutralNPC != nil {
		t.Fatal("experiment enabled by default")
	}
	h.Config.ExperimentalNeutralNPC = true
	requestNeutralNPC(t, h, owner)
	if err := h.battleMessage(owner, owner.game(), ready(owner)); err != nil {
		t.Fatal(err)
	}
	if r.hasPVEActor(100) {
		t.Fatal("missing replica admitted")
	}
	bad := ready(peer)
	protocol.WriteUint32(bad.Payload, 47, 6)
	if err := h.battleMessage(peer, peer.game(), bad); err == nil {
		t.Fatal("stale receipt accepted")
	}
	if r.hasPVEActor(100) {
		t.Fatal("stale replica admitted")
	}
	if err := h.battleMessage(peer, peer.game(), ready(peer)); err != nil {
		t.Fatal(err)
	}
	if !r.hasPVEActor(100) || !r.controlsBattleActor(owner, 100) || r.controlsBattleActor(peer, 100) || r.hasPVEActor(101) {
		t.Fatal("controller or actor isolation failed")
	}
	m := combatPacket(protocol.BattleEventMovement, 108, 100, 0, 0)
	protocol.WriteUint32(m.Payload, 15, 1)
	if err := h.battleMessage(owner, owner.game(), m); err != nil {
		t.Fatal(err)
	}
	roomOutputs(t, peer, 8071)
	if err := h.battleMessage(peer, peer.game(), m); err == nil {
		t.Fatal("peer forged NPC movement")
	}
	roomOutputs(t, owner)
	roomOutputs(t, outsider)
	r.Owner = peer.UID
	if r.hasPVEActor(100) {
		t.Fatal("old controller survived host change")
	}
	r.Owner = owner.UID
	r.Serial++
	if r.hasPVEActor(100) {
		t.Fatal("old NPC survived new round")
	}
}

func TestNeutralNPCStatusLocalOnly(t *testing.T) {
	h, owner, _, _ := combatFixture()
	h.Config.ExperimentalNeutralNPC = true
	owner.Room.Request[46] = byte(protocol.TeamDeathmatch)
	for _, remote := range []string{"127.0.0.1:9999", "192.0.2.1:9999"} {
		req := httptest.NewRequest("GET", "/experimental/neutral-npc?room=1&serial=7", nil)
		req.RemoteAddr = remote
		w := httptest.NewRecorder()
		h.neutralNPCStatus(w, req)
		if remote == "127.0.0.1:9999" {
			if w.Code != 200 || len(w.Body.Bytes()) != 24 || protocol.ReadUint64(w.Body.Bytes(), 8) != owner.UID {
				t.Fatal("invalid local status")
			}
		} else if w.Code != 403 {
			t.Fatal("remote status exposed")
		}
	}
}

func TestNeutralNPCPeerRelayReceipt(t *testing.T) {
	h, owner, peer, _ := combatFixture()
	h.Config.ExperimentalNeutralNPC = true
	r := owner.Room
	r.Request[46] = byte(protocol.TeamSurvival)
	requestNeutralNPC(t, h, owner)
	for _, s := range []*Session{owner, peer} {
		m := extendedPacket(neutralNPCReady, 51, s.UID, 100, 43)
		raw, err := protocol.Encode(m)
		if err != nil {
			t.Fatal(err)
		}
		h.observeRelayedBattle(s, raw, map[uint64]bool{owner.UID: true, peer.UID: true})
	}
	if !r.hasNeutralNPC(100) {
		t.Fatal("relayed native receipts not registered")
	}
	roomOutputs(t, owner)
	roomOutputs(t, peer)
}

func TestNeutralNPCPlanAuthorizationAndFailure(t *testing.T) {
	h, owner, peer, _ := combatFixture()
	h.Config.ExperimentalNeutralNPC = true
	r := owner.Room
	r.Request[46] = byte(protocol.TeamSurvival)
	bad := extendedPacket(neutralNPCRequest, 67, peer.UID, 100, 43)
	if err := h.battleMessage(peer, peer.game(), bad); err == nil || r.NeutralNPC != nil {
		t.Fatal("peer created plan")
	}
	ready := extendedPacket(neutralNPCReady, 51, peer.UID, 100, 43)
	if err := h.battleMessage(peer, peer.game(), ready); err == nil {
		t.Fatal("unsolicited ready admitted")
	}
	m := requestNeutralNPC(t, h, owner)
	deadline := r.NeutralNPC.deadline
	protocol.WriteUint32(m.Payload, 55, 0x41A00000)
	if err := h.battleMessage(owner, owner.game(), m); err != nil {
		t.Fatal(err)
	}
	if r.NeutralNPC.position[0] != 0x41200000 || r.NeutralNPC.deadline != deadline {
		t.Fatal("duplicate changed plan")
	}
	for _, remote := range []string{"192.0.2.1:12", "127.0.0.1:12"} {
		req := httptest.NewRequest("GET", "/experimental/neutral-npc-plan?room=1&serial=7", nil)
		req.RemoteAddr = remote
		w := httptest.NewRecorder()
		h.neutralNPCStatus(w, req)
		if remote == "127.0.0.1:12" {
			p := w.Body.Bytes()
			if w.Code != 200 || len(p) != 192 || protocol.ReadUint32(p, 16) != 1 || protocol.ReadUint32(p, 32) != 0x41200000 || protocol.ReadUint32(p, 44) != 2 {
				t.Fatal("invalid spawn plan", w.Code)
			}
		} else if w.Code != 403 {
			t.Fatal("plan exposed remotely")
		}
	}
	r.refreshNeutralNPC(deadline.Add(time.Second))
	if !r.NeutralNPC.cancelled || r.hasNeutralNPC(100) || r.NeutralNPC.failed[peer.UID] != 4 {
		t.Fatal("timeout not cancelled")
	}
	if err := h.battleMessage(peer, peer.game(), ready); err == nil {
		t.Fatal("late receipt revived cancelled plan")
	}
}

func TestNeutralNPCWorkerFailure(t *testing.T) {
	h, owner, peer, _ := combatFixture()
	h.Config.ExperimentalNeutralNPC = true
	owner.Room.Request[46] = byte(protocol.TeamSurvival)
	requestNeutralNPC(t, h, owner)
	req := httptest.NewRequest("POST", fmt.Sprintf("/experimental/neutral-npc-failure?room=1&serial=7&uid=%d&code=2", peer.UID), nil)
	req.RemoteAddr = "192.0.2.1:1"
	w := httptest.NewRecorder()
	h.neutralNPCFailure(w, req)
	if w.Code != 403 || owner.Room.NeutralNPC.cancelled {
		t.Fatal("remote failure admitted")
	}
	req.RemoteAddr = "127.0.0.1:1"
	w = httptest.NewRecorder()
	h.neutralNPCFailure(w, req)
	if w.Code != 204 || !owner.Room.NeutralNPC.cancelled || owner.Room.hasNeutralNPC(100) {
		t.Fatal("failure did not block AI")
	}
}

func TestNeutralNPCLeaveBeforeAndAfterReady(t *testing.T) {
	for _, ready := range []bool{false, true} {
		h, owner, peer, _ := combatFixture()
		h.Config.ExperimentalNeutralNPC = true
		owner.Room.Request[46] = byte(protocol.TeamSurvival)
		requestNeutralNPC(t, h, owner)
		if ready {
			for _, s := range []*Session{owner, peer} {
				if err := h.battleMessage(s, s.game(), extendedPacket(neutralNPCReady, 51, s.UID, 100, 43)); err != nil {
					t.Fatal(err)
				}
			}
		}
		delete(owner.Room.Members, peer.UID)
		if got := owner.Room.hasNeutralNPC(100); got != ready {
			t.Fatalf("ready=%v, ordinary departure active=%v", ready, got)
		}
		if ready {
			owner.Room.Owner = peer.UID
			if owner.Room.hasNeutralNPC(100) {
				t.Fatal("AI survived owner change")
			}
		}
	}
}

func TestNeutralNPCMultipleIdentitiesAndAdmission(t *testing.T) {
	for _, options := range [][3]uint32{{5, 0, 0}, {2, 4, 0}, {2, 0, 2}, {4, 2, 1}} {
		h, owner, peer, _ := combatFixture()
		h.Config.ExperimentalNeutralNPC = true
		r := owner.Room
		r.Request[46] = byte(protocol.TeamSurvival)
		m := extendedPacket(neutralNPCRequest, 79, owner.UID, 100, 43)
		for i, v := range options {
			protocol.WriteUint32(m.Payload, 67+i*4, v)
		}
		err := h.battleMessage(owner, owner.game(), m)
		if options != [3]uint32{4, 2, 1} {
			if err == nil || r.NeutralNPC != nil {
				t.Fatalf("invalid options accepted: %v", options)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for uid := uint64(100); uid < 104; uid++ {
			if r.hasNeutralNPC(uid) {
				t.Fatal("NPC active before receipts")
			}
		}
		for _, s := range []*Session{owner, peer} {
			ack := extendedPacket(neutralNPCReady, 63, s.UID, 100, 43)
			for i, v := range options {
				protocol.WriteUint32(ack.Payload, 51+i*4, v)
			}
			if err := h.battleMessage(s, s.game(), ack); err != nil {
				t.Fatal(err)
			}
		}
		for uid := uint64(100); uid < 104; uid++ {
			if !r.hasNeutralNPC(uid) || !r.controlsBattleActor(owner, uid) || r.controlsBattleActor(peer, uid) {
				t.Fatalf("wrong authority for %d", uid)
			}
		}
		if r.hasNeutralNPC(99) || r.hasNeutralNPC(104) {
			t.Fatal("unknown monster admitted")
		}
		req := httptest.NewRequest("GET", "/experimental/neutral-npc-plan?room=1&serial=7", nil)
		req.RemoteAddr = "127.0.0.1:1"
		w := httptest.NewRecorder()
		h.neutralNPCStatus(w, req)
		p := w.Body.Bytes()
		if len(p) != 192 || protocol.ReadUint32(p, 0) != 0x3343504e || protocol.ReadUint32(p, 52) != 4 || protocol.ReadUint32(p, 56) != 2 || protocol.ReadUint32(p, 60) != 1 {
			t.Fatal("NPC3 metadata mismatch")
		}
	}
}

func TestModStatusOwnRoomAndPVEOnly(t *testing.T) {
	h, owner, peer, outsider := combatFixture()
	h.Config.ExperimentalNeutralNPC = true
	r := owner.Room
	r.Request[46] = byte(protocol.TeamSurvival)
	p := h.modStatus(peer)
	if len(p) != 216 || protocol.ReadUint64(p, 8) != peer.UID || protocol.ReadUint64(p, 24) != owner.UID || protocol.ReadUint32(p, 208) != 0 {
		t.Fatal("wrong team status")
	}
	outsider.Room = nil
	if protocol.ReadUint32(h.modStatus(outsider), 64) != 0 {
		t.Fatal("outsider sees room")
	}
	r.Request[46] = byte(protocol.StageAssault)
	if protocol.ReadUint32(h.modStatus(peer), 208) != 1 {
		t.Fatal("PVE not enabled")
	}
	r.Members[peer.UID].Spectator = true
	if protocol.ReadUint32(h.modStatus(peer), 208) != 0 {
		t.Fatal("spectator enabled")
	}
	r.Members[peer.UID].Spectator = false
	r.Stage = "settlement"
	if protocol.ReadUint32(h.modStatus(peer), 208) != 0 {
		t.Fatal("settlement enabled")
	}
	h.Config.ExperimentalNeutralNPC = false
	if protocol.ReadUint32(h.modStatus(peer), 212) != 0 {
		t.Fatal("server switch ignored")
	}
}

func TestOnlineModDoesNotExposeLegacyHTTP(t *testing.T) {
	h, _, _, _ := combatFixture()
	h.Config.ExperimentalNeutralNPC = true
	server := &Server{Hub: h}
	request := httptest.NewRequest("GET", "/experimental/neutral-npc-plan?room=1&serial=7", nil)
	request.RemoteAddr = "127.0.0.1:9999"
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, request)
	if w.Code != 404 {
		t.Fatalf("legacy HTTP exposed through proxy: %d", w.Code)
	}
}
