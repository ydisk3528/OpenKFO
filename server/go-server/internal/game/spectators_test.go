package game

import (
	"bytes"
	"fmt"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"testing"
	"time"
)

func observerFixture(t *testing.T) (*Hub, *Session, *Session, *Session) {
	t.Helper()
	h, a, b, o := waitingRoomFixture()
	h.Store = recoveryStore(t)
	r := a.Room
	r.SpectatorCapacity = 2
	r.Request[34] = 1
	for _, s := range []*Session{a, b, o} {
		s.Bound = true
		s.P2PUntil = time.Now().Add(time.Minute)
	}
	if err := h.installAs(r, o, true); err != nil {
		t.Fatal(err)
	}
	roomOutputs(t, o, 3100, 3160, 3105)
	roomOutputs(t, a, 3090)
	roomOutputs(t, b, 3090)
	return h, a, b, o
}
func TestObserverAdmissionAndWire(t *testing.T) {
	h, a, b, o := observerFixture(t)
	r := a.Room
	if r.fighterCount() != 2 || r.observerCount() != 1 || !r.isObserver(o) || r.Members[o.UID].Slot != spectatorSlot {
		t.Fatal("spectator consumed a fighter slot")
	}
	raw := roomList(r)
	if raw[34] != 1 || protocol.ReadUint16(raw, 35) != 2 || protocol.ReadUint16(raw, 37) != 1 || raw[40] != 2 {
		t.Fatal("directory spectator fields")
	}
	rec := fighter(persistence.Account{UID: o.UID, Profile: make([]byte, 360)}, r.Members[o.UID])
	entry := roomEntryForMember(r, r.Members[o.UID], rec)
	if rec[76] != 1 || entry[64] != 2 || entry[10] != spectatorSlot || entry[96+76] != 1 {
		t.Fatal("spectator roster fields")
	}
	roomRequest(t, h, o, protocol.MsgReady, nil)
	roomOutputs(t, a)
	roomOutputs(t, b)
	roomOutputs(t, o)
	if r.Members[o.UID].Ready {
		t.Fatal("observer became ready")
	}
	r.Members[a.UID].Ready = true
	r.Members[b.UID].Ready = true
	h.beginNetworkProbe(r)
	defer h.cancelNetworkProbe(r)
	if r.NetworkProbe == nil {
		t.Fatal("observer blocked preparation")
	}
	roomOutputs(t, a, 4150)
	roomOutputs(t, b, 4150)
	roomOutputs(t, o)
}
func TestObserverToggleCapacityAndOwner(t *testing.T) {
	h, a, b, o := observerFixture(t)
	r := a.Room
	roomRequest(t, h, o, protocol.MsgToggleSpectator, nil)
	for _, s := range []*Session{a, b, o} {
		p := roomOutputs(t, s, 3092)[0].Payload
		if len(p) != 165 || p[12] != 0 || p[16+76] != 0 {
			t.Fatal("fighter toggle layout")
		}
	}
	if r.fighterCount() != 3 || r.Members[o.UID].Slot != 2 {
		t.Fatal("toggle did not allocate free slot")
	}
	roomRequest(t, h, a, protocol.MsgToggleSpectator, nil)
	for _, s := range []*Session{a, b, o} {
		roomOutputs(t, s, 3092, 3160)
	}
	if r.Owner != b.UID || !r.isObserver(a) {
		t.Fatal("host not transferred to fighter")
	}
	r.Request[37] = 2
	roomRequest(t, h, a, protocol.MsgToggleSpectator, nil)
	p := roomOutputs(t, a, 3092)[0].Payload
	if len(p) != 12 || int32(protocol.ReadUint32(p, 8)) != -1 || !r.isObserver(a) {
		t.Fatal("full fighter capacity mutated observer")
	}
	r.Stage = "battle"
	roomRequest(t, h, a, protocol.MsgToggleSpectator, nil)
	if int32(protocol.ReadUint32(roomOutputs(t, a, 3092)[0].Payload, 8)) != -3 {
		t.Fatal("battle toggle admitted")
	}
}
func TestObserverLoadingAndLeave(t *testing.T) {
	for _, leaves := range []bool{false, true} {
		h, a, b, o := observerFixture(t)
		r := a.Room
		r.Stage = "loading"
		for _, s := range []*Session{a, b, o} {
			s.game().Phase = "loading"
		}
		roomRequest(t, h, a, 4160, nil)
		roomRequest(t, h, b, 4160, nil)
		for _, s := range []*Session{a, b, o} {
			roomOutputs(t, s, 4170, 4170)
		}
		if r.Stage != "loading" {
			t.Fatal("did not wait for observer resources")
		}
		if leaves {
			h.leave(o, true)
			roomOutputs(t, o, 3115)
			for _, s := range []*Session{a, b} {
				roomOutputs(t, s, 3130, 4180)
			}
		} else {
			roomRequest(t, h, o, 4160, nil)
			roomOutputs(t, o, 4170)
			for _, s := range []*Session{a, b} {
				roomOutputs(t, s, 4170, 4180)
			}
		}
		if r.Stage != "wait_ready" {
			t.Fatal("observer resource barrier stuck")
		}
		for _, s := range []*Session{a, b} {
			p := make([]byte, 14)
			protocol.WriteUint16(p, 0, r.ID)
			protocol.WriteUint64(p, 2, s.UID)
			roomRequest(t, h, s, 8040, p)
		}
		for _, s := range []*Session{a, b} {
			roomOutputs(t, s, 8070, 8090)
		}
		if !leaves {
			roomOutputs(t, o, 8070, 8090)
			h.leave(o, true)
			roomOutputs(t, o, 3115)
			roomOutputs(t, a, 3130)
			roomOutputs(t, b, 3130)
		}
		if r.Stage != "battle" {
			t.Fatal("observer leave ended battle")
		}
		h.Mutex.Lock()
		r.Stage = "room"
		h.Mutex.Unlock()
	}
}
func TestObserverCannotSendEffectsOrReceiveRewards(t *testing.T) {
	h, a, b, o := observerFixture(t)
	r := a.Room
	r.Stage = "battle"
	for _, s := range []*Session{a, b, o} {
		s.game().Phase = "battle"
	}
	msg := combatPacket(8127, 63, o.UID, o.UID, 55)
	if err := h.battleMessage(o, o.game(), msg); err != nil {
		t.Fatal(err)
	}
	if err := h.consume(o, o.game(), protocol.Message{ID: 4200, Payload: protocol.Uint32Bytes(1)}); err != nil {
		t.Fatal(err)
	}
	if err := h.switchWeapon(o, o.game(), nil); err != nil {
		t.Fatal(err)
	}
	report := settlementReport(r)
	if err := h.settleReport(o, report); err != nil || len(r.Reports) != 0 {
		t.Fatal("observer voted")
	}
	for _, s := range []*Session{a, b, o} {
		roomOutputs(t, s)
	}
	if _, err := validateBattleReport(r, report); err != nil {
		t.Fatal("observer added to report quorum", err)
	}
	rewards := []persistence.BattleReward{{UID: a.UID, Profile: bytes.Repeat([]byte{1}, 360)}, {UID: b.UID, Profile: bytes.Repeat([]byte{2}, 360)}}
	p := settlementPacket(r, rewards, o.UID).Payload
	if len(p) != 1000 || !bytes.Equal(p[140:500], make([]byte, 360)) || !bytes.Equal(p[640:1000], make([]byte, 360)) {
		t.Fatal("observer got another account profile")
	}
}
func TestRoomDirectoryWaitingFilter(t *testing.T) {
	h, a, _, o := waitingRoomFixture()
	a.Room.Stage = "battle"
	for _, option := range []byte{0, 1, 2} {
		handled, err := h.roomMessage(o, o.game(), protocol.Message{ID: 2260, Payload: []byte{1, option, 0x88}})
		if !handled {
			t.Fatal("not handled")
		}
		if option == 2 {
			if err == nil {
				t.Fatal("invalid filter accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		p := roomOutputs(t, o, 2280)[0].Payload
		if (len(p)-8)/259 != int(option) {
			t.Fatal("waiting filter ignored")
		}
	}
}
func TestScoreOutcomesUseKillsAndPeerAgreement(t *testing.T) {
	for _, mode := range []protocol.RoomType{protocol.SoloDeathmatch, protocol.TeamDeathmatch} {
		_, a, b, _ := waitingRoomFixture()
		r := a.Room
		r.Request[46] = byte(mode)
		p := settlementReport(r)
		p[8] = 1
		p[87+8] = 3
		r.Reports = map[uint64][]byte{a.UID: p, b.UID: bytes.Clone(p)}
		result := battleOutcomes(r)
		if result[a.UID] != "loss" || result[b.UID] != "win" {
			t.Fatal("score match used health", result)
		}
		r.Reports[b.UID][8] = 9
		if battleOutcomes(r)[a.UID] != "unconfirmed" {
			t.Fatal("disagreed score granted win")
		}
	}
}

func TestObserverResultAckBarrier(t *testing.T) {
	for _, leave := range []bool{false, true} {
		h, a, b, o := observerFixture(t)
		r := a.Room
		r.Stage = "settlement"
		for _, s := range []*Session{a, b, o} {
			s.game().Phase = "settlement"
		}
		roomRequest(t, h, a, 4115, make([]byte, 4))
		roomRequest(t, h, b, 4115, make([]byte, 4))
		if r.Stage != "settlement" {
			t.Fatal("result did not wait for observer")
		}
		if leave {
			h.leave(o, false)
		} else {
			roomRequest(t, h, o, 4115, make([]byte, 4))
		}
		if r.Stage != "room" || !r.canConfigure(a) {
			t.Fatal("result acknowledgement did not release room")
		}
		for _, s := range []*Session{a, b} {
			if leave {
				roomOutputs(t, s, 3130, 4070, 4070)
			} else {
				roomOutputs(t, s, 4070, 4070)
			}
		}
		if !leave {
			roomOutputs(t, o, 4070, 4070)
			roomRequest(t, h, o, 4115, make([]byte, 4))
			roomOutputs(t, o)
		}
	}
}

func TestObserverTogglePreservesOtherPlayersReady(t *testing.T) {
	h, a, b, o := observerFixture(t)
	r := a.Room
	r.Members[b.UID].Ready = true
	for i := 0; i < 2; i++ {
		roomRequest(t, h, o, protocol.MsgToggleSpectator, nil)
		for _, s := range []*Session{a, b, o} {
			roomOutputs(t, s, protocol.MsgSpectatorChanged)
		}
		if !r.Members[b.UID].Ready {
			t.Fatal("unrelated fighter lost readiness")
		}
	}
	// Ready fighters must explicitly cancel readiness before changing roles.
	roomRequest(t, h, b, protocol.MsgToggleSpectator, nil)
	out := roomOutputs(t, b, protocol.MsgSpectatorChanged)[0]
	if int32(protocol.ReadUint32(out.Payload, 8)) != -3 || !r.Members[b.UID].Ready || r.Members[b.UID].Spectator {
		t.Fatal("ready fighter changed roles")
	}
	roomOutputs(t, a)
	roomOutputs(t, o)
	r.Members[b.UID].Ready = false
	roomRequest(t, h, b, protocol.MsgToggleSpectator, nil)
	for _, s := range []*Session{a, b, o} {
		roomOutputs(t, s, protocol.MsgSpectatorChanged)
	}
	if !r.Members[b.UID].Spectator {
		t.Fatal("unready fighter could not spectate")
	}

}

func TestObserverOwnerTransferOnlyClearsNewOwner(t *testing.T) {
	h, a, b, o := observerFixture(t)
	roomRequest(t, h, o, protocol.MsgToggleSpectator, nil)
	for _, s := range []*Session{a, b, o} {
		roomOutputs(t, s, protocol.MsgSpectatorChanged)
	}
	r := a.Room
	r.Members[b.UID].Ready = true
	r.Members[o.UID].Ready = true
	roomRequest(t, h, a, protocol.MsgToggleSpectator, nil)
	for _, s := range []*Session{a, b, o} {
		out := roomOutputs(t, s, protocol.MsgSpectatorChanged, protocol.MsgPlayerNotReady, protocol.MsgRoomOwner)
		if protocol.ReadUint64(out[1].Payload, 0) != b.UID {
			t.Fatal("wrong new owner")
		}
	}
	if r.Members[b.UID].Ready || !r.Members[o.UID].Ready {
		t.Fatal("owner readiness scope")
	}
}

func TestObserverToggleRejectsReadyAndStartingRoom(t *testing.T) {
	for _, stage := range []string{"room", "loading", "wait_ready", "battle", "settlement"} {
		for _, spectator := range []bool{false, true} {
			t.Run(stage+fmt.Sprint(spectator), func(t *testing.T) {
				h, host, peer, _ := waitingRoomFixture()
				r := host.Room
				r.Stage, r.Request[34], r.SpectatorCapacity = stage, 1, 2
				m := r.Members[peer.UID]
				m.Spectator, m.Ready = spectator, !spectator
				probe := &roomNetworkProbe{pending: map[uint64]*Session{peer.UID: peer}}
				r.NetworkProbe = probe
				before := *m
				roomRequest(t, h, peer, protocol.MsgToggleSpectator, nil)
				p := roomOutputs(t, peer, protocol.MsgSpectatorChanged)[0].Payload
				if len(p) != 12 || int32(protocol.ReadUint32(p, 8)) != -3 {
					t.Fatal("late toggle accepted")
				}
				if r.Members[peer.UID] != m || m.Spectator != before.Spectator || m.Ready != before.Ready || m.Slot != before.Slot || m.Spawn != before.Spawn || r.NetworkProbe != probe || r.Owner != host.UID {
					t.Fatal("rejected toggle changed room")
				}
				roomOutputs(t, host)
			})
		}
	}
	h, _, peer, _ := waitingRoomFixture()
	peer.Room.Members[peer.UID].Ready = true
	roomRequest(t, h, peer, protocol.MsgToggleSpectator, nil)
	if int32(protocol.ReadUint32(roomOutputs(t, peer, protocol.MsgSpectatorChanged)[0].Payload, 8)) != -3 {
		t.Fatal("ready player toggled before probe")
	}
}
