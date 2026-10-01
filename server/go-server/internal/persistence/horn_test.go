package persistence

import (
	"bytes"
	"kungfu.local/server/internal/protocol"
	"testing"
)

func hornCard(id uint32, n uint16) []byte {
	p := make([]byte, 68)
	p[4] = 71
	protocol.WriteUint32(p, 0, id)
	protocol.WriteUint32(p, 5, 713001)
	protocol.WriteUint16(p, 23, n)
	return p
}
func TestHornCards(t *testing.T) {
	a, b := hornCard(1, 2), hornCard(2, 8)
	p, e := spendHornCards([][]byte{a, b}, 10)
	if e != nil || len(p) != 2 || protocol.ReadUint16(p[0], 23) != 0 || protocol.ReadUint16(p[1], 23) != 0 {
		t.Fatal(p, e)
	}
	if protocol.ReadUint16(a, 23) != 2 || protocol.ReadUint16(b, 23) != 8 {
		t.Fatal("mutated source before transaction")
	}
	for _, edit := range []func([]byte){func(p []byte) { protocol.WriteUint32(p, 5, 713002) }, func(p []byte) { p[4] = 74 }, func(p []byte) { protocol.WriteUint32(p, 19, 2) }, func(p []byte) { protocol.WriteUint16(p, 23, 7) }} {
		q := bytes.Clone(b)
		edit(q)
		if _, e := spendHornCards([][]byte{a, q}, 10); e != ErrHornCards {
			t.Fatal("wrong/expired/insufficient card accepted", e)
		}
	}
	if _, e := spendHornCards([][]byte{hornCard(1, 65535)}, 3); e != nil {
		t.Fatal(e)
	}
	if _, e := spendHornCards([][]byte{a}, 0); e == nil {
		t.Fatal("invalid cost")
	}
	for k, n := range map[uint32]uint16{2480: 1, 2486: 3, 2481: 10, 5002: 0} {
		if HornCost(k) != n {
			t.Fatal(k)
		}
	}
}
