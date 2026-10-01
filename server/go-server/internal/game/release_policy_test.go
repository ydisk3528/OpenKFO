package game

import (
	"kungfu.local/server/internal/protocol"
	"testing"
)

func TestOldReleaseLobbyNoticeAndBattleGate(t *testing.T) {
	h, a, b, _ := combatFixture()
	h.Config.RequiredClientRelease = "oss-current"
	a.ClientRelease = "oss-current"
	b.ClientRelease = "old"
	h.warnOldRelease(b)
	h.warnOldRelease(b)
	if len(b.Output) != 1 {
		t.Fatal("missing or repeated lobby notice")
	}
	<-b.Output
	if h.roomReleaseReady(a.Room) {
		t.Fatal("old peer allowed battle")
	}
	if len(a.Output) != 1 || len(b.Output) != 1 {
		t.Fatal("host or old peer not informed")
	}
	b.ClientRelease = "oss-current"
	if !h.roomReleaseReady(a.Room) {
		t.Fatal("current version blocked")
	}
	h.releaseVersion.Store("oss-next")
	if !h.outdatedRelease(a) {
		t.Fatal("OSS version did not replace legacy requirement")
	}
	if m := notice(OldReleaseNotice); m.ID != 20150 || len(m.Payload) != 215 || protocol.ReadUint32(m.Payload, 0) != 0 {
		t.Fatal("invalid chat notice")
	}
}

func TestMatchingVersionRequiresActualConfigHash(t *testing.T) {
	h, a, b, _ := combatFixture()
	h.Config.RequiredClientRelease = "release-1"
	h.Config.ConfigHash = "actual-config"
	a.ClientRelease, b.ClientRelease = "release-1", "release-1"
	a.ClientConfigHash = "actual-config"
	for _, hash := range []string{"", "stale-config"} {
		b.ClientConfigHash = hash
		if !h.outdatedRelease(b) || h.roomReleaseReady(a.Room) {
			t.Fatal("version label bypassed file check")
		}
	}
	b.ClientConfigHash = "actual-config"
	if !h.roomReleaseReady(a.Room) {
		t.Fatal("matching release and file blocked")
	}
	for len(b.Output) > 0 {
		<-b.Output
	}
	h.warnOldRelease(b)
	h.warnOldRelease(b)
	if len(b.Output) != 1 {
		t.Fatal("current version notice missing or repeated")
	}
	roomOutputs(t, b, 20150)
}
