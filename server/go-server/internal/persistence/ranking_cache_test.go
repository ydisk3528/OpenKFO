package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRankingCacheCoalescesAndPreservesProtocol(t *testing.T) {
	s := &Store{}
	var calls atomic.Int32
	load := func() ([]rankingEntry, error) {
		calls.Add(1)
		time.Sleep(time.Millisecond)
		return []rankingEntry{{7, []byte("first"), 100}, {9, []byte("second"), 50}}, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.rankingEntries(0, load); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("loads=%d", calls.Load())
	}
	// A nil DB proves both users use the common snapshot, with their own rank.
	a, ownA, err := s.Rankings(7, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, ownB, err := s.Rankings(9, 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) || len(a) != 54 || len(ownA) != 5 || ownA[1] != 0 || ownB[1] != 1 {
		t.Fatal("protocol or per-player rank changed")
	}
	if _, _, err = s.Rankings(7, 255); !errors.Is(err, ErrDenied) {
		t.Fatal("invalid category accepted")
	}
	s.rankings[0].expires = time.Now().Add(-time.Second)
	if _, err = s.rankingEntries(0, load); err != nil || calls.Load() != 2 {
		t.Fatal("expiration failed", err)
	}
}

func TestRankingRedisDownFallsBack(t *testing.T) {
	s := &Store{}
	close, err := s.ConfigureRankingRedis("127.0.0.1:1", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	start := time.Now()
	calls := 0
	load := func() ([]rankingEntry, error) { calls++; return []rankingEntry{{1, []byte("a"), 1}}, nil }
	if _, err = s.rankingEntries(0, load); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || s.rankingRedis.retryAfter.Load() <= time.Now().UnixNano() {
		t.Fatal("missing fallback/cooldown")
	}
	if time.Since(start) > time.Second {
		t.Fatal("Redis blocked fallback")
	}
	s.rankings[0].expires = time.Time{}
	if _, err = s.rankingEntries(0, func() ([]rankingEntry, error) { return nil, errors.New("database down") }); err == nil {
		t.Fatal("database failure hidden")
	}
}

func TestRankingRedisIntegration(t *testing.T) {
	addr := os.Getenv("OPENKFO_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("dedicated Redis integration endpoint not set")
	}
	namespace := fmt.Sprintf("test_rank_%d", time.Now().UnixNano())
	makeStore := func() *Store {
		s := &Store{}
		close, err := s.ConfigureRankingRedis(addr, os.Getenv("OPENKFO_TEST_REDIS_PASSWORD_FILE"), namespace)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(close)
		return s
	}
	s := makeStore()
	key := s.rankingRedis.prefix + "0"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer s.rankingRedis.client.Del(context.Background(), key)
	if err := s.rankingRedis.client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	load := func() ([]rankingEntry, error) { return []rankingEntry{{42, []byte("tester"), 123}}, nil }
	if _, err := s.rankingEntries(0, load); err != nil {
		t.Fatal(err)
	}
	ttl, err := s.rankingRedis.client.TTL(ctx, key).Result()
	if err != nil || ttl <= 0 || ttl > rankingTTL {
		t.Fatal("TTL", ttl, err)
	}
	other := makeStore()
	entries, err := other.rankingEntries(0, func() ([]rankingEntry, error) { t.Fatal("Redis hit queried database"); return nil, nil })
	if err != nil || len(entries) != 1 || entries[0].UID != 42 {
		t.Fatal(entries, err)
	}
	// Corrupt data is disposable and rebuilt from source.
	if err = s.rankingRedis.client.Set(ctx, key, "bad-json", rankingTTL).Err(); err != nil {
		t.Fatal(err)
	}
	third := makeStore()
	n := 0
	_, err = third.rankingEntries(0, func() ([]rankingEntry, error) { n++; return load() })
	if err != nil || n != 1 {
		t.Fatal("corrupt cache not rebuilt", err)
	}
	isolated := &Store{}
	close, err := isolated.ConfigureRankingRedis(addr, os.Getenv("OPENKFO_TEST_REDIS_PASSWORD_FILE"), namespace+"_offline")
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	defer isolated.rankingRedis.client.Del(context.Background(), isolated.rankingRedis.prefix+"0")
	n = 0
	_, err = isolated.rankingEntries(0, func() ([]rankingEntry, error) { n++; return load() })
	if err != nil || n != 1 {
		t.Fatal("namespace leaked", err)
	}
}
