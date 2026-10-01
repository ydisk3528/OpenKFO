package game

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
)

const hornQueueLimit = 128
const hornBatchSize = 128

type hornTarget struct {
	session        *Session
	channel, lobby uint32
}
type hornShard struct {
	sync.RWMutex
	all     map[*Session]hornTarget
	lobbies map[uint32]map[*Session]hornTarget
}
type hornJob struct {
	sender   hornTarget
	message  protocol.Message
	text, id string
	kind     uint32
}

// Owns an online-only index and a bounded FIFO. One drain worker is active at a
// time; it exits when idle. No database or socket waits under the world lock.
type HornManager struct {
	hub     *Hub
	shards  [32]hornShard
	mu      sync.Mutex
	jobs    []hornJob
	running bool
}

func (h *Hub) hornManager() *HornManager {
	if h.Horns == nil {
		h.Horns = &HornManager{hub: h}
	}
	return h.Horns
}

// Membership methods are called under Hub.Mutex, only on login/channel changes.
func (m *HornManager) join(s *Session) {
	shard := &m.shards[s.UID%32]
	shard.Lock()
	defer shard.Unlock()
	if shard.all == nil {
		shard.all = make(map[*Session]hornTarget)
		shard.lobbies = make(map[uint32]map[*Session]hornTarget)
	}
	if old, ok := shard.all[s]; ok {
		delete(shard.lobbies[old.lobby], s)
		if len(shard.lobbies[old.lobby]) == 0 {
			delete(shard.lobbies, old.lobby)
		}
	}
	t := hornTarget{s, s.GameChannel, s.LobbyID}
	shard.all[s] = t
	if shard.lobbies[t.lobby] == nil {
		shard.lobbies[t.lobby] = make(map[*Session]hornTarget)
	}
	shard.lobbies[t.lobby][s] = t
}
func (h *Hub) removeHornSession(s *Session) {
	if h.Horns == nil {
		return
	}
	shard := &h.Horns.shards[s.UID%32]
	shard.Lock()
	defer shard.Unlock()
	if old, ok := shard.all[s]; ok {
		delete(shard.all, s)
		delete(shard.lobbies[old.lobby], s)
		if len(shard.lobbies[old.lobby]) == 0 {
			delete(shard.lobbies, old.lobby)
		}
	}
}
func (m *HornManager) submit(job hornJob) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.jobs) >= hornQueueLimit {
		return false
	}
	m.jobs = append(m.jobs, job)
	if !m.running {
		m.running = true
		go m.drain()
	}
	return true
}
func (m *HornManager) drain() {
	for {
		m.mu.Lock()
		if len(m.jobs) == 0 {
			m.running = false
			m.mu.Unlock()
			return
		}
		job := m.jobs[0]
		m.jobs[0] = hornJob{}
		m.jobs = m.jobs[1:]
		m.mu.Unlock()
		m.process(job)
	}
}

// Native 7DCF50/7DD080 use 215 bytes; 7DCE.. uses 257 for mood.
// Dispatch table AF174F..AF1839: 2485/2487/2482 -> A289D0/A288D0/A287D0.
func parseHorn(kind uint32, p []byte, uid uint64, name string) (protocol.Message, string, error) {
	size, lo, to, response := 215, 12, 13, uint32(2485)
	if kind == 2486 {
		response = 2487
	} else if kind == 2481 {
		size, lo, to, response = 257, 50, 56, 2482
	} else if kind != 2480 {
		return protocol.Message{}, "", protocol.ErrFrame
	}
	if len(p) != size {
		return protocol.Message{}, "", protocol.ErrFrame
	}
	n := int(p[lo])
	if n < 2 || n > 200 || to+n > len(p) || p[to+n-1] != 0 || bytes.IndexByte(p[to:to+n-1], 0) >= 0 {
		return protocol.Message{}, "", protocol.ErrFrame
	}
	text, err := persistence.DecodeGBK(p[to : to+n-1])
	if err != nil || strings.TrimSpace(text) == "" {
		return protocol.Message{}, "", protocol.ErrFrame
	}
	for _, c := range text {
		if unicode.IsControl(c) {
			return protocol.Message{}, "", protocol.ErrFrame
		}
	}
	for _, b := range p[to+n:] {
		if b != 0 {
			return protocol.Message{}, "", protocol.ErrFrame
		}
	}
	payload := make([]byte, 256)
	outText := 55
	if kind == 2481 {
		if p[51] > 2 {
			return protocol.Message{}, "", protocol.ErrFrame
		}
		payload = make([]byte, 257)
		outText = 56
		copy(payload[51:56], p[51:56])
	}
	protocol.WriteUint64(payload, 0, uid)
	copy(payload[8:28], persistence.GBK(name))
	payload[50] = byte(n)
	copy(payload[outText:], p[to:to+n])
	return protocol.Message{ID: response, Payload: payload}, text, nil
}
func (h *Hub) horn(s *Session, msg protocol.Message) error {
	reply, text, err := parseHorn(msg.ID, msg.Payload, s.UID, s.Nickname)
	if err != nil {
		s.sendGame(notice("喇叭消息格式不正确，请重新输入。"))
		return nil
	}
	if s.HornPending || time.Since(s.LastHorn) < 2*time.Second {
		s.sendGame(notice("喇叭正在处理或发送过快，请稍后再试。"))
		return nil
	}
	if s.LoggedOut || s.GameChannel == 0 || s.LobbyID == 0 || h.Store == nil {
		s.sendGame(notice("当前不能发送喇叭，请进入频道后重试。"))
		return nil
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		s.sendGame(notice("喇叭服务暂不可用，未扣卡。"))
		return nil
	}
	job := hornJob{hornTarget{s, s.GameChannel, s.LobbyID}, reply, text, hex.EncodeToString(id[:]), msg.ID}
	if !h.hornManager().submit(job) {
		s.sendGame(notice("喇叭队列已满，未扣卡，请稍后重试。"))
		return nil
	}
	s.HornPending = true
	s.LastHorn = time.Now()
	return nil
}
func (m *HornManager) live(t hornTarget) bool {
	s := t.session
	if m.hub.Sessions[s.UID] != s || s.LoggedOut || s.GameChannel != t.channel || s.LobbyID != t.lobby || s.game() == nil {
		return false
	}
	select {
	case <-s.Done:
		return false
	default:
	}
	return true
}
func (m *HornManager) process(job hornJob) {
	h, s := m.hub, job.sender.session
	h.Mutex.RLock()
	live := m.live(job.sender)
	h.Mutex.RUnlock()
	defer func() { h.Mutex.Lock(); s.HornPending = false; h.Mutex.Unlock() }()
	if !live {
		return
	}
	// CheckText can consult MySQL; do it in this worker, outside the game lock.
	if err := h.Store.CheckText(job.text); err != nil {
		m.tell(job.sender, moderationNotice(err))
		return
	}
	changed, err := h.Store.InventoryManager().ConsumeHorn(s.UID, job.kind, job.id, job.text)
	if err != nil {
		if errors.Is(err, persistence.ErrHornDisabled) {
			m.tell(job.sender, "此类喇叭已被管理员暂停，未发送、未扣卡。")
		} else if errors.Is(err, persistence.ErrHornCards) {
			m.tell(job.sender, fmt.Sprintf("小喇叭卡不足，本次需要 %d 张，未发送。", persistence.HornCost(job.kind)))
		} else {
			log.Printf("horn_debit_failed uid=%d event=%s error=%v", s.UID, job.id, err)
			m.tell(job.sender, "喇叭处理未确认，请核对背包；本次不会自动重试扣卡。")
		}
		return
	}
	h.Mutex.Lock()
	if m.live(job.sender) {
		s.refreshRevision++
		for _, p := range changed {
			id := protocol.ReadUint32(p, 0)
			if s.Inventory == nil {
				s.Inventory = make(map[uint32][]byte)
			}
			if protocol.ReadUint32(p, 19) == 0xffffffff {
				delete(s.Inventory, id)
				s.sendGame(protocol.Message{ID: 2162, Payload: protocol.Uint32Bytes(id)})
			} else {
				s.Inventory[id] = bytes.Clone(p)
				s.sendGame(protocol.Message{ID: 2161, Payload: bytes.Clone(p)})
			}
		}
	}
	h.Mutex.Unlock()
	queued, skipped := m.broadcast(job)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = h.Store.DB.ExecContext(ctx, `UPDATE horn_events SET status='dispatched',queued=?,skipped=? WHERE id=?`, queued, skipped, job.id)
	log.Printf("horn_dispatched event=%s uid=%d kind=%d lobby=%d cost=%d queued=%d skipped=%d audit_error=%v", job.id, s.UID, job.kind, job.sender.lobby, persistence.HornCost(job.kind), queued, skipped, err)
}
func (m *HornManager) tell(t hornTarget, text string) {
	m.hub.Mutex.RLock()
	defer m.hub.Mutex.RUnlock()
	if m.live(t) {
		t.session.send(t.channel, notice(text))
	}
}
func (m *HornManager) broadcast(job hornJob) (queued, skipped int) {
	encoded, err := protocol.Encode(job.message)
	if err != nil {
		return
	}
	for i := range m.shards {
		shard := &m.shards[i]
		shard.RLock()
		entries := shard.all
		if job.kind == 2480 {
			entries = shard.lobbies[job.sender.lobby]
		}
		targets := make([]hornTarget, 0, len(entries))
		for _, t := range entries {
			targets = append(targets, t)
		}
		shard.RUnlock()
		for start := 0; start < len(targets); start += hornBatchSize {
			// Bound registry lock duration. All network writes remain on existing writers.
			m.hub.Mutex.RLock()
			for _, t := range targets[start:min(start+hornBatchSize, len(targets))] {
				if !m.live(t) {
					skipped++
					continue
				}
				if emitHorn(t.session, t.channel, encoded) {
					queued++
				} else {
					skipped++
				}
			}
			m.hub.Mutex.RUnlock()
			runtime.Gosched()
		}
	}
	return
}

// Reserve half the queue for combat/control. A congested recipient misses this
// live-only horn; never close their game or retry into a later login.
func emitHorn(s *Session, channel uint32, data []byte) bool {
	if len(s.Output) >= cap(s.Output)/2 {
		return false
	}
	select {
	case <-s.Done:
		return false
	default:
	}
	n := int64(len(data) + 128)
	if s.queuedBytes.Add(n) > 1024*1024 {
		s.queuedBytes.Add(-n)
		return false
	}
	select {
	case s.Output <- tunnel.Frame{Op: "data", Channel: channel, Data: data}:
		return true
	default:
		s.queuedBytes.Add(-n)
		return false
	}
}
