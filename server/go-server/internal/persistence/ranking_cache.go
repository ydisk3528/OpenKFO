package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const rankingTTL = 30 * time.Second

type rankingEntry struct {
	UID   uint64
	Name  []byte
	Score int32
}
type rankingSlot struct {
	sync.Mutex
	entries []rankingEntry
	expires time.Time
}
type rankingSnapshot struct {
	Entries []rankingEntry
	Expires time.Time
}
type rankingRedis struct {
	client     *redis.Client
	prefix     string
	retryAfter atomic.Int64
}

// ConfigureRankingRedis is called once, before accepting players. An empty
// address disables Redis; the bounded process-local cache remains available.
func (s *Store) ConfigureRankingRedis(addr, passwordFile, namespace string) (func(), error) {
	if addr == "" {
		return func() {}, nil
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(namespace) {
		return nil, errors.New("Redis namespace required (separate online and offline)")
	}
	var password string
	if passwordFile != "" {
		b, err := os.ReadFile(passwordFile)
		if err != nil {
			return nil, errors.New("cannot read Redis password file")
		}
		password = strings.TrimSpace(string(b))
	}
	c := redis.NewClient(&redis.Options{Addr: addr, Password: password, Protocol: 2, MaxRetries: -1,
		DialTimeout: 100 * time.Millisecond, ReadTimeout: 100 * time.Millisecond, WriteTimeout: 100 * time.Millisecond,
		PoolTimeout: 100 * time.Millisecond, PoolSize: 4, ContextTimeoutEnabled: true})
	s.rankingRedis = &rankingRedis{client: c, prefix: "openkfo:" + namespace + ":rankings:v1:"}
	log.Print("ranking_cache Redis configured; TTL=30s; failures fall back to MySQL")
	return func() { _ = c.Close() }, nil
}

func (r *rankingRedis) failed(err error) {
	if err == nil || errors.Is(err, redis.Nil) {
		return
	}
	now := time.Now().UnixNano()
	old := r.retryAfter.Load()
	if old <= now && r.retryAfter.CompareAndSwap(old, time.Now().Add(5*time.Second).UnixNano()) {
		// Do not print Redis errors: server replies may contain sensitive data.
		log.Print("ranking_cache Redis unavailable; bypassing for 5s; MySQL fallback")
	}
}

func (s *Store) rankingEntries(category byte, load func() ([]rankingEntry, error)) ([]rankingEntry, error) {
	slot := &s.rankings[category]
	// Per-category lock coalesces misses; never a battle or global room lock.
	slot.Lock()
	defer slot.Unlock()
	if time.Now().Before(slot.expires) {
		return slot.entries, nil
	}
	r := s.rankingRedis
	available := func() bool { return r != nil && time.Now().UnixNano() >= r.retryAfter.Load() }
	key := ""
	if r != nil {
		key = fmt.Sprintf("%s%d", r.prefix, category)
	}
	if available() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		b, err := r.client.Get(ctx, key).Bytes()
		cancel()
		r.failed(err)
		var v rankingSnapshot
		if err == nil && len(b) <= 32<<20 && json.Unmarshal(b, &v) == nil && validRankingSnapshot(v) {
			slot.entries, slot.expires = v.Entries, v.Expires
			return slot.entries, nil
		}
	}
	entries, err := load()
	if err != nil {
		return nil, err
	}
	slot.entries, slot.expires = entries, time.Now().Add(rankingTTL)
	if available() {
		b, err := json.Marshal(rankingSnapshot{entries, slot.expires})
		if err == nil && len(b) <= 32<<20 {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			err = r.client.Set(ctx, key, b, rankingTTL).Err()
			cancel()
			r.failed(err)
		}
	}
	return entries, nil
}

func validRankingSnapshot(v rankingSnapshot) bool {
	if !v.Expires.After(time.Now()) || v.Expires.After(time.Now().Add(rankingTTL)) {
		return false
	}
	seen := make(map[uint64]bool, len(v.Entries))
	for i, e := range v.Entries {
		if seen[e.UID] || len(e.Name) > 20 {
			return false
		}
		seen[e.UID] = true
		if i > 0 {
			prev := v.Entries[i-1]
			if prev.Score < e.Score || (prev.Score == e.Score && prev.UID >= e.UID) {
				return false
			}
		}
	}
	return true
}
