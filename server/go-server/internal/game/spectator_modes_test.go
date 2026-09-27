package game

import (
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"testing"
	"time"
)

func TestObserverAdmissionAndToggleAllRoomModes(t *testing.T) {
	for _, mode := range []protocol.RoomType{protocol.SoloSurvival, protocol.TeamSurvival, protocol.SoloDeathmatch, protocol.TeamDeathmatch, protocol.NewPlayerGuide, protocol.FreePractice, protocol.FosterMode, protocol.RebornMode, protocol.StageAssault} {
		t.Run(mode.String(), func(t *testing.T) {
			h, host, peer, observer := waitingRoomFixture()
			h.Store = recoveryStore(t)
			r := host.Room
			r.Request[46], r.Request[34], r.SpectatorCapacity = byte(mode), 1, 2
			observer.Bound, observer.P2PUntil = true, time.Now().Add(time.Minute)
			if err := h.installAs(r, observer, true); err != nil {
				t.Fatal(err)
			}
			roomOutputs(t, observer, 3100, 3160, 3105)
			roomOutputs(t, host, 3090)
			roomOutputs(t, peer, 3090)
			if r.fighterCount() != 2 || !r.isObserver(observer) {
				t.Fatal("observer used fighter slot")
			}
			roomRequest(t, h, peer, protocol.MsgToggleSpectator, nil)
			for _, s := range []*Session{host, peer, observer} {
				roomOutputs(t, s, 3092)
			}
			if !r.isObserver(peer) || r.fighterCount() != 1 {
				t.Fatal("mode blocked observer toggle")
			}
			r.Request[34] = 0
			if r.observerLimit() != 0 {
				t.Fatal("disabled observer flag ignored")
			}
		})
	}
}

func TestFosterObserverResultExcludesObserverAward(t *testing.T) {
	_, host, peer, observer := observerFixture(t)
	r := host.Room
	r.Request[46] = byte(protocol.FosterMode)
	awards := []persistence.BattleReward{}
	for _, s := range []*Session{host, peer} {
		awards = append(awards, persistence.BattleReward{UID: s.UID, Outcome: persistence.StageOutcomeClear, Profile: make([]byte, protocol.RoleProfileSize)})
	}
	if _, err := fosterResultPacket(r, awards, observer.UID); err != nil {
		t.Fatal(err)
	}
	awards[1].UID = observer.UID
	if _, err := fosterResultPacket(r, awards, observer.UID); err == nil {
		t.Fatal("observer reward accepted")
	}
}
