package game

import "testing"

func TestHealthReceiptWindow(t *testing.T) {
	var previous battleSequence
	seen := false
	for _, step := range []struct {
		seq    uint32
		accept bool
	}{
		{0xfffffffe, true}, {0, true}, {0xffffffff, true},
		{0xffffffff, false}, {0, false}, {0xfffffffe, false},
		{63, true}, {1, true}, {1, false}, {0xffffffff, false},
		{200, true}, {137, true}, {136, false}, {200, false},
	} {
		next, ok := acceptHealthSequence(previous, seen, step.seq)
		if ok != step.accept {
			t.Fatalf("seq=%d accepted=%v want=%v", step.seq, ok, step.accept)
		}
		if ok {
			previous = next
			seen = true
		}
	}
}
