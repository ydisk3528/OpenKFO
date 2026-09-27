package game

import (
	"bytes"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"testing"
)

func TestPlayerDirectoryPagesAndLifecycle(t *testing.T) {
	hub := NewHub(nil, Config{})
	for uid := uint64(1); uid <= 16; uid++ {
		hub.Sessions[uid] = &Session{UID: uid, GameChannel: 3, Channels: map[uint32]*Channel{3: {ID: 3, Phase: "lobby"}}, Done: make(chan struct{})}
	}
	hub.Sessions[13].Channels[3].Phase = "room"
	hub.Sessions[14].LoggedOut = true
	hub.Sessions[15].GameChannel = 0 // channel handoff / authentication only
	close(hub.Sessions[16].Done)
	for _, tc := range []struct{ page, size, first, count, pages, actual uint32 }{
		{1, 10, 1, 10, 2, 1}, {2, 10, 11, 3, 2, 2}, {1, 7, 1, 7, 2, 1}, {2, 7, 8, 6, 2, 2}, {0xffffffff, 7, 8, 6, 2, 2},
	} {
		ids, h, err := hub.playerPage(0, append(protocol.Uint32Bytes(tc.page), protocol.Uint32Bytes(tc.size)...))
		if err != nil || len(ids) != int(tc.count) || ids[0] != uint64(tc.first) || protocol.ReadUint32(h, 4) != tc.pages || protocol.ReadUint32(h, 0) != tc.actual {
			t.Fatalf("page %+v: ids=%v header=%x err=%v", tc, ids, h, err)
		}
		for i := 1; i < len(ids); i++ {
			if ids[i] <= ids[i-1] {
				t.Fatal("unstable order")
			}
		}
	}
	for _, payload := range [][]byte{nil, make([]byte, 7), make([]byte, 9), make([]byte, 8), append(protocol.Uint32Bytes(1), protocol.Uint32Bytes(0xffffffff)...)} {
		if _, _, err := hub.playerPage(0, payload); err == nil {
			t.Fatal("malformed page accepted")
		}
	}
	hub.Sessions = map[uint64]*Session{}
	ids, h, err := hub.playerPage(0, append(protocol.Uint32Bytes(1), protocol.Uint32Bytes(10)...))
	if err != nil || len(ids) != 0 || protocol.ReadUint32(h, 4) != 1 {
		t.Fatal("empty directory")
	}
}

func TestPlayerListRecordNativeFields(t *testing.T) {
	profile := make([]byte, 360)
	profile[122] = 2
	protocol.WriteUint16(profile, persistence.LevelOffset, 150)
	p, err := playerListRecord(10001, "测试玩家", profile)
	if err != nil || len(p) != 68 || protocol.ReadUint64(p, 0) != 10001 || p[29] != 2 || p[30] != 150 {
		t.Fatalf("record: %x %v", p, err)
	}
	if !bytes.Equal(p[8:16], persistence.GBK("测试玩家")) || p[16] != 0 {
		t.Fatal("GBK nickname")
	}
	for _, name := range []string{"", "角色😀", "name\x00suffix", "超过十个汉字的昵称不能截断"} {
		if _, err := playerListRecord(10001, name, profile); err == nil {
			t.Fatalf("invalid name %q", name)
		}
	}
	if _, err := playerListRecord(10001, "valid", profile[:359]); err == nil {
		t.Fatal("short profile")
	}
	profile[122] = 0
	if _, err := playerListRecord(10001, "valid", profile); err == nil {
		t.Fatal("pending character listed")
	}
}

func TestPlayerDirectoryIncludesEveryRoomPhase(t *testing.T) {
	hub := NewHub(nil, Config{})
	phases := []string{"lobby", "room", "loading", "battle", "settlement", "wait_ready", "authenticated", "handoff", "closed", "profile_sent"}
	for i, phase := range phases {
		uid := uint64(i + 1)
		hub.Sessions[uid] = &Session{UID: uid, LobbyID: 2, GameChannel: 3, Channels: map[uint32]*Channel{3: {ID: 3, Phase: phase}}, Done: make(chan struct{})}
	}
	hub.Sessions[20] = &Session{UID: 20, LobbyID: 3, GameChannel: 3, Channels: map[uint32]*Channel{3: {ID: 3, Phase: "battle"}}, Done: make(chan struct{})}
	request := append(protocol.Uint32Bytes(1), protocol.Uint32Bytes(7)...)
	ids, _, err := hub.playerPage(2, request)
	if err != nil || len(ids) != 6 {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	for i, uid := range ids {
		if uid != uint64(i+1) {
			t.Fatalf("missing room phase: %v", ids)
		}
	}
	hub.Sessions[3].LoggedOut = true
	close(hub.Sessions[4].Done)
	ids, _, err = hub.playerPage(2, request)
	if err != nil || len(ids) != 4 {
		t.Fatalf("disconnected players listed: %v %v", ids, err)
	}
}
