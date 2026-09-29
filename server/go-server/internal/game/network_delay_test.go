package game

import (
	"kungfu.local/server/internal/protocol"
	"testing"
	"time"
)

func TestNetworkProbeLifecycle(t *testing.T) {
	h, owner, peer, _ := waitingRoomFixture()
	r := owner.Room
	for _, m := range r.Members {
		m.Ready = true
	}
	h.beginNetworkProbe(r)
	p := r.NetworkProbe
	defer h.cancelNetworkProbe(r)
	roomOutputs(t, owner, protocol.MsgNetworkDelayProbe)
	roomOutputs(t, peer, protocol.MsgNetworkDelayProbe)
	h.beginNetworkProbe(r)
	roomOutputs(t, owner)
	if r.NetworkProbe != p {
		t.Fatal("duplicate probe replaced timer")
	}
	p.started = time.Now().Add(-25 * time.Millisecond)
	if err := h.networkDelayReply(owner, nil); err != nil {
		t.Fatal(err)
	}
	delay := r.Members[owner.UID].NetworkDelay
	if delay < 25 || len(p.pending) != 1 || r.Stage != "room" {
		t.Fatal("invalid sample or premature start")
	}
	if err := h.networkDelayReply(owner, nil); err != nil || r.Members[owner.UID].NetworkDelay != delay {
		t.Fatal("duplicate reply changed sample")
	}
	if err := h.networkDelayReply(peer, []byte{1}); err == nil {
		t.Fatal("nonempty reply accepted")
	}
	h.expireNetworkProbe(r, p)
	if r.NetworkProbe != nil || r.Members[owner.UID].Ready || r.Members[peer.UID].Ready || r.Stage != "room" {
		t.Fatal("timeout did not restore readiness")
	}
	if err := h.networkDelayReply(peer, nil); err != nil {
		t.Fatal(err)
	}
	if r.Members[peer.UID].NetworkDelay != 0 {
		t.Fatal("late reply measured")
	}
	// An obsolete timer cannot cancel a later attempt.
	for _, m := range r.Members {
		m.Ready = true
	}
	h.beginNetworkProbe(r)
	newProbe := r.NetworkProbe
	h.expireNetworkProbe(r, p)
	if r.NetworkProbe != newProbe {
		t.Fatal("obsolete timeout cancelled new attempt")
	}
}

func TestNetworkProbeCancelledOnLeave(t *testing.T) {
	h, owner, peer, _ := waitingRoomFixture()
	r := owner.Room
	for _, m := range r.Members {
		m.Ready = true
	}
	h.beginNetworkProbe(r)
	h.leave(peer, true)
	if r.NetworkProbe != nil {
		t.Fatal("leave retained probe")
	}
}

func TestNetworkProbeReadyGateAndCancellation(t *testing.T) {
	h, owner, peer, _ := waitingRoomFixture()
	r := owner.Room
	h.beginNetworkProbe(r)
	if r.NetworkProbe != nil {
		t.Fatal("unready players were probed")
	}
	for _, m := range r.Members {
		m.Ready = true
	}
	h.beginNetworkProbe(r)
	defer h.cancelNetworkProbe(r)
	impostor := &Session{UID: peer.UID, Room: r}
	if err := h.networkDelayReply(impostor, nil); err != nil || len(r.NetworkProbe.pending) != 2 {
		t.Fatal("wrong session consumed reply")
	}
	probe := r.NetworkProbe
	roomOutputs(t, owner, protocol.MsgNetworkDelayProbe)
	roomOutputs(t, peer, protocol.MsgNetworkDelayProbe)
	for _, session := range []*Session{owner, peer} {
		session.P2PUntil = time.Now().Add(time.Minute)
		for _, id := range []uint32{protocol.MsgReady, protocol.MsgCancelReady} {
			roomRequest(t, h, session, id, nil)
			if r.NetworkProbe != probe || !r.Members[session.UID].Ready || len(probe.pending) != 2 {
				t.Fatal("ready/cancel changed pending start")
			}
			roomOutputs(t, owner)
			roomOutputs(t, peer)
		}
	}
	// Timeout still unlocks the room; normal waiting-room cancellation works.
	h.expireNetworkProbe(r, probe)
	roomOutputs(t, owner, protocol.MsgPlayerNotReady, protocol.MsgPlayerNotReady, 20150)
	roomOutputs(t, peer, protocol.MsgPlayerNotReady, protocol.MsgPlayerNotReady, 20150)
	if r.NetworkProbe != nil {
		t.Fatal("timeout retained start lock")
	}
	r.Members[peer.UID].Ready = true
	roomRequest(t, h, peer, protocol.MsgCancelReady, nil)
	if r.Members[peer.UID].Ready {
		t.Fatal("normal cancellation blocked")
	}
	roomOutputs(t, owner, protocol.MsgPlayerNotReady)
	roomOutputs(t, peer, protocol.MsgPlayerNotReady)
}
