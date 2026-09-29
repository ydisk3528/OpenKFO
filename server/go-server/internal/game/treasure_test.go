package game

import (
	"bytes"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"testing"
)

func TestNativeTreasurePreviewLayout(t *testing.T) {
	six := make([]persistence.TreasurePrize, 6)
	for i := range six {
		six[i] = persistence.TreasurePrize{Weight: 1, RewardBundle: persistence.RewardBundle{Gold: uint32(i + 1)}}
	}
	p, err := encodeTreasurePreview(six, nil, 19, 2)
	if err != nil || len(p) != 271 {
		t.Fatal(len(p), err)
	}
	if protocol.ReadUint32(p, 267) != 19 {
		t.Fatal("serial")
	}
	for i := 0; i < 6; i++ {
		if protocol.ReadUint32(p, 3+i*35+14) != 4 || protocol.ReadUint32(p, 3+i*35+18) != uint32(i+1) {
			t.Fatal("reward layout")
		}
		if protocol.ReadUint32(p, 213+i*9+5) != uint32(2*(i+1)) {
			t.Fatal("cost layout")
		}
	}
	six[0].Tickets = 1
	if _, err = encodeTreasurePreview(six, nil, 19, 2); err == nil {
		t.Fatal("mixed display accepted")
	}
}

func TestTreasureItemDurationAndCount(t *testing.T) {
	weapon := make([]byte, 68)
	weapon[4] = 25
	protocol.WriteUint32(weapon, 5, 253037)
	protocol.WriteUint32(weapon, 13, 168)
	protocol.WriteUint32(weapon, 19, 1)
	ticket := make([]byte, 68)
	ticket[4] = 60
	protocol.WriteUint32(ticket, 5, 603316)
	protocol.WriteUint32(ticket, 19, 1)
	protocol.WriteUint16(ticket, 23, 10)
	defs := []persistence.ItemDefinition{{Key: 1, Record: weapon, Days: 7}, {Key: 2, Record: ticket}}
	six := make([]persistence.TreasurePrize, 6)
	for i := range six {
		six[i] = persistence.TreasurePrize{Weight: 1, RewardBundle: persistence.RewardBundle{Items: []uint32{uint32(i%2 + 1)}}}
	}
	p, err := encodeTreasurePreview(six, defs, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if protocol.ReadUint32(p, 3+18) != 1 || protocol.ReadUint32(p, 38+18) != 2 {
		t.Fatal("item ID used instead of reward catalog key")
	}
	if protocol.ReadUint32(p, 3+22) != 168 || protocol.ReadUint32(p, 3+26) != 0 || protocol.ReadUint32(p, 38+22) != 0 || protocol.ReadUint32(p, 38+26) != 10 {
		t.Fatal("duration/count confused with inventory state")
	}
}

func TestTreasureDrawShuffledRequestAndResult(t *testing.T) {
	six := make([]persistence.TreasurePrize, 6)
	for i := range six {
		six[i] = persistence.TreasurePrize{Weight: 1, RewardBundle: persistence.RewardBundle{Gold: uint32(i + 1)}}
	}
	preview, err := encodeTreasurePreview(six, nil, 13, 1)
	if err != nil {
		t.Fatal(err)
	}
	p := make([]byte, 30)
	p[0] = 1
	p[1] = 4
	protocol.WriteUint32(p, 26, 13)
	for i, id := range []uint32{4, 1, 6, 3, 2, 5} {
		protocol.WriteUint32(p, 2+i*4, id)
	}
	if !validTreasureDrawRequest(p, preview) {
		t.Fatal("native shuffled request rejected")
	}
	r := treasureResultPacket(preview, p, 5)
	if len(r) != 258 || r[0] != 4 || protocol.ReadUint32(r, 18) != 6 || protocol.ReadUint32(r, 31) != 13 || r[44+4*35+5] != 1 {
		t.Fatal("result mismatch")
	}
	protocol.WriteUint32(p, 2, 1)
	if validTreasureDrawRequest(p, preview) {
		t.Fatal("duplicate IDs accepted")
	}
	if validTreasureDrawRequest(nil, preview) {
		t.Fatal("close treated as draw")
	}
}

func TestTreasureSixConsecutiveFlipsKeepDisplayedRewards(t *testing.T) {
	six := make([]persistence.TreasurePrize, 6)
	for i := range six {
		six[i] = persistence.TreasurePrize{Weight: 1, RewardBundle: persistence.RewardBundle{Gold: uint32(10 + i)}}
	}
	preview, _ := encodeTreasurePreview(six, nil, 17, 1)
	state := &treasureDrawState{Preview: preview, Cards: append([]byte(nil), preview[3:213]...), Replies: map[string][]byte{}}
	opened := uint32(0)
	for turn := 0; turn < 6; turn++ {
		p := make([]byte, 30)
		p[0] = 1
		p[1] = byte(turn)
		protocol.WriteUint32(p, 26, 17)
		for i := 0; i < 6; i++ {
			protocol.WriteUint32(p, 2+i*4, uint32(i+1))
		}
		opened |= 1 << turn
		out := persistence.TreasureDrawResult{Winner: 5 - turn, OpenedMask: opened}
		r, err := state.result(p, out)
		if err != nil {
			t.Fatal(turn, err)
		}
		if protocol.ReadUint32(r, 18) != uint32(15-turn) {
			t.Fatal("winner display differs from awarded prize")
		}
		for i := 0; i < 6; i++ {
			cell := r[44+i*35 : 44+(i+1)*35]
			if protocol.ReadUint32(cell, 1) != uint32(i+1) {
				t.Fatal("card identity changed")
			}
			if (cell[5] != 0) != (i <= turn) {
				t.Fatal("open card disappeared")
			}
		}
		again, err := state.result(p, out)
		if err != nil || !bytes.Equal(r, again) {
			t.Fatal("retry display changed")
		}
	}
}
