package game

import (
	"bytes"
	"kungfu.local/server/internal/protocol"
	"testing"
)

func TestGMNoticeTargetsOnlineGameSessions(t *testing.T) {
	h, a, b, c := combatFixture()
	b.LoggedOut = true
	c.Close()
	room := a.Room
	if n := h.sendNotice("线下测试通知"); n != 1 {
		t.Fatalf("recipients=%d", n)
	}
	want, _ := protocol.Encode(notice("线下测试通知"))
	if !bytes.Equal((<-a.Output).Data, want) {
		t.Fatal("wrong notice protocol")
	}
	if len(b.Output) != 0 || len(c.Output) != 0 {
		t.Fatal("sent to logged out or closed player")
	}
	if a.Room != room {
		t.Fatal("notice changed room")
	}
	select {
	case <-a.Done:
		t.Fatal("notice disconnected player")
	default:
	}
}
