package persistence

import "testing"

func TestHornSettingsIndependent(t *testing.T) {
	for i, kind := range []uint32{2480, 2486, 2481} {
		r := HornSettings{ChannelEnabled: i == 0, RealmEnabled: i == 1, MoodEnabled: i == 2}
		for _, other := range []uint32{2480, 2486, 2481, 0, 5002} {
			if r.Allows(other) != (kind == other) {
				t.Fatalf("%d affects %d", kind, other)
			}
		}
	}
}
