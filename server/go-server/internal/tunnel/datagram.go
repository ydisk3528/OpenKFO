package tunnel

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"sync"
)

// Datagram keys and routing IDs are issued only over the authenticated TLS session.
// A direction-specific nonce and a 64-packet replay window allow UDP reordering.
const DatagramLimit = 1200
const datagramHeader = 30

var ErrDatagram = errors.New("invalid secure datagram")

type DatagramGrant struct {
	Order bool   `json:"order,omitempty"`
	Drain bool   `json:"drain_ack,omitempty"`
	ID    []byte `json:"id"`
	Key   []byte `json:"key"`
	Port  int    `json:"port"`
}
type DatagramCodec struct {
	mu                  sync.Mutex
	id                  [16]byte
	aead                cipher.AEAD
	direction           uint32
	sent, highest, seen uint64
}

func NewDatagramGrant(port int) (*DatagramGrant, error) {
	g := &DatagramGrant{ID: make([]byte, 16), Key: make([]byte, 32), Port: port, Drain: true}
	if _, e := rand.Read(g.ID); e != nil {
		return nil, e
	}
	if _, e := rand.Read(g.Key); e != nil {
		return nil, e
	}
	return g, nil
}
func NewDatagramCodec(g *DatagramGrant, server bool) (*DatagramCodec, error) {
	if g == nil || len(g.ID) != 16 || len(g.Key) != 32 || g.Port < 1 || g.Port > 65535 {
		return nil, ErrDatagram
	}
	block, e := aes.NewCipher(g.Key)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	c := &DatagramCodec{aead: a}
	copy(c.id[:], g.ID)
	if server {
		c.direction = 1
	}
	return c, nil
}
func DatagramID(p []byte) (id [16]byte, ok bool) {
	if len(p) < datagramHeader+16 || len(p) > DatagramLimit || string(p[:4]) != "KFU1" {
		return id, false
	}
	copy(id[:], p[4:20])
	return id, true
}
func (c *DatagramCodec) Seal(port uint16, data []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(data)+datagramHeader+c.aead.Overhead() > DatagramLimit || c.sent == ^uint64(0) {
		return nil, ErrDatagram
	}
	c.sent++
	p := make([]byte, datagramHeader)
	copy(p, "KFU1")
	copy(p[4:20], c.id[:])
	binary.BigEndian.PutUint64(p[20:28], c.sent)
	binary.BigEndian.PutUint16(p[28:30], port)
	var nonce [12]byte
	binary.BigEndian.PutUint32(nonce[:4], c.direction)
	copy(nonce[4:], p[20:28])
	return c.aead.Seal(p, nonce[:], data, p), nil
}
func (c *DatagramCodec) Open(p []byte) (uint16, []byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := DatagramID(p)
	if !ok || id != c.id {
		return 0, nil, ErrDatagram
	}
	seq := binary.BigEndian.Uint64(p[20:28])
	if seq == 0 || (seq <= c.highest && (c.highest-seq >= 64 || c.seen&(uint64(1)<<(c.highest-seq)) != 0)) {
		return 0, nil, ErrDatagram
	}
	var nonce [12]byte
	binary.BigEndian.PutUint32(nonce[:4], c.direction^1)
	copy(nonce[4:], p[20:28])
	data, e := c.aead.Open(nil, nonce[:], p[datagramHeader:], p[:datagramHeader])
	if e != nil {
		return 0, nil, ErrDatagram
	}
	if seq > c.highest {
		shift := seq - c.highest
		if shift >= 64 {
			c.seen = 0
		} else {
			c.seen <<= shift
		}
		c.highest = seq
		c.seen |= 1
	} else {
		c.seen |= uint64(1) << (c.highest - seq)
	}
	return binary.BigEndian.Uint16(p[28:30]), data, nil
}
