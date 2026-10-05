package game

type battleSequence struct {
	Sequence   uint32
	Payload    string
	HealthSeen uint64
}

// Bounded receipt window, per sender and target. The high water mark never
// moves backwards. Replays older than 64 event numbers remain rejected; this
// does not recover missing packets or claim to correct final client HP.
func acceptHealthSequence(previous battleSequence, seen bool, sequence uint32) (battleSequence, bool) {
	if !seen {
		return battleSequence{Sequence: sequence, HealthSeen: 1}, true
	}
	delta := int32(sequence - previous.Sequence)
	if delta > 0 {
		previous.HealthSeen = previous.HealthSeen<<uint32(delta) | 1
		previous.Sequence = sequence
		return previous, true
	}
	age := previous.Sequence - sequence
	if age >= 64 || previous.HealthSeen&(uint64(1)<<age) != 0 {
		return previous, false
	}
	previous.HealthSeen |= uint64(1) << age
	return previous, true
}
