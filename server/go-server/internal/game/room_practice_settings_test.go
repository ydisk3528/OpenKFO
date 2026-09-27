package game

import (
	"bytes"
	"encoding/hex"
	"testing"

	"kungfu.local/server/internal/protocol"
)

func TestPracticeMapChangeCapturedZeroDuration(t *testing.T) {
	// test001, 2026-09-24 17:29:18: native 3200/48, map 99, unlimited practice.
	payload, err := hex.DecodeString("630000006300000000000100000074657374303031b5c4d1b5c1b7cad200000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	hub, host, peer, _ := waitingRoomFixture()
	room := host.Room
	room.Request[46] = byte(protocol.FreePractice)
	protocol.WriteUint16(room.Request, 47, 0)
	hub.Config.Pools["5:4"] = []uint32{804, 99}
	roomRequest(t, hub, host, 3200, payload)
	if protocol.ReadUint32(room.Request, 38) != 99 || protocol.ReadUint16(room.Request, 47) != 0 {
		t.Fatal("practice map change was not committed")
	}
	for _, player := range []*Session{host, peer} {
		messages := roomOutputs(t, player, 3220)
		if !bytes.Equal(messages[0].Payload, payload) {
			t.Fatal("map update was not broadcast intact")
		}
	}
	room.Request[46] = byte(protocol.TeamSurvival)
	if _, err := applyRoomSettings(room.Request, payload); err == nil {
		t.Fatal("unlimited practice duration accepted in team survival")
	}
}
