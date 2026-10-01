package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
	"os"
	"time"
)

// Operator-only occupancy clients. No combat AI, no host-start packet, no reconnect.
func roomBots() error {
	var q struct {
		Config   string
		Room     uint16
		Owner    uint64
		LobbyID  uint32
		Password string
	}
	if e := json.NewDecoder(os.Stdin).Decode(&q); e != nil {
		return e
	}
	if q.Room == 0 || q.Owner == 0 || len(q.Password) > 10 {
		return fmt.Errorf("invalid room target")
	}
	var clients []*client
	defer func() {
		for _, c := range clients {
			c.conn.Close()
		}
	}()
	known := map[uint64]bool{q.Owner: true}
	var team byte
	for i := 0; i < 5; i++ {
		var secret [16]byte
		if _, e := rand.Read(secret[:]); e != nil {
			return e
		}
		account := fmt.Sprintf("r3bot%d%d", time.Now().Unix(), i)
		c, e := connect(command{Config: q.Config, Account: account, Password: hex.EncodeToString(secret[:]), CharacterName: fmt.Sprintf("测试占位%d", i+1), LobbyID: q.LobbyID})
		if e != nil {
			return e
		}
		clients = append(clients, c)
		known[c.uid] = true
		p := make([]byte, 14)
		protocol.WriteUint16(p, 0, q.Room)
		copy(p[3:], q.Password)
		if e = c.game(3, 3070, p); e != nil {
			return e
		}
		joined, roster := false, false
		for n := 0; n < 64 && (!joined || !roster); n++ {
			_, messages, e := c.read()
			if e != nil {
				return e
			}
			for _, m := range messages {
				if m.ID == 3080 {
					return fmt.Errorf("room join rejected")
				}
				if m.ID == 3100 {
					if len(m.Payload) != 245 || protocol.ReadUint64(m.Payload, 2) != q.Owner || m.Payload[62] != 6 || !protocol.RoomType(m.Payload[65]).IsTeam() {
						return fmt.Errorf("target must be owner's six-player team room")
					}
					joined = true
				}
				if m.ID == 3105 {
					rows, e := protocol.ParseRoomMembers(m.Payload)
					if e != nil {
						return e
					}
					host := false
					for _, r := range rows {
						if !known[r.UID()] {
							return fmt.Errorf("real player present; refusing five bots")
						}
						if r.UID() == q.Owner {
							team = r.Raw[10]
							host = true
						}
					}
					if !host {
						return fmt.Errorf("owner missing")
					}
					roster = true
				}
			}
		}
		if !joined || !roster {
			return fmt.Errorf("room verification incomplete")
		}
		target := team
		if i >= 2 {
			target = 1 - team
		}
		if e = c.game(3, 3230, []byte{target}); e != nil {
			return e
		}
		changed := false
		for n := 0; n < 64 && !changed; n++ {
			_, ms, e := c.read()
			if e != nil {
				return e
			}
			for _, m := range ms {
				if m.ID == 3250 && len(m.Payload) == 10 && protocol.ReadUint64(m.Payload, 0) == c.uid {
					if m.Payload[8] != target {
						return fmt.Errorf("team mismatch")
					}
					changed = true
				}
			}
		}
		if !changed {
			return fmt.Errorf("team acknowledgement missing")
		}
		emit(output{Kind: "bot_joined", UID: c.uid, Text: fmt.Sprintf("test bot %d team=%d", i+1, target)})
	}
	errors := make(chan error, 5)
	for _, c := range clients {
		if e := c.game(3, 4030, nil); e != nil {
			return e
		}
		go func(c *client) {
			for {
				_, ms, e := c.read()
				if e != nil {
					errors <- e
					return
				}
				for _, m := range ms {
					switch m.ID {
					case 4080:
						e = c.game(3, 4160, nil)
					case 4180:
						p := make([]byte, 14)
						protocol.WriteUint16(p, 0, q.Room)
						protocol.WriteUint64(p, 2, c.uid)
						e = c.game(3, 8040, p)
					case 3130:
						if len(m.Payload) == 8 && protocol.ReadUint64(m.Payload, 0) == q.Owner {
							errors <- fmt.Errorf("owner left; bots stopped")
							return
						}
					case 3115:
						errors <- fmt.Errorf("bot removed; group stopped")
						return
					}
					if e != nil {
						errors <- e
						return
					}
				}
			}
		}(c)
	}
	emit(output{Kind: "bots_ready_requested", Text: "5 bots joined; ready requested; owner starts manually"})
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	timeout := time.NewTimer(time.Hour)
	defer timeout.Stop()
	for {
		select {
		case e := <-errors:
			return e
		case <-timeout.C:
			return nil
		case <-ticker.C:
			for _, c := range clients {
				if e := c.send(tunnel.Frame{Op: "ping"}); e != nil {
					return e
				}
				if e := c.keepP2P(); e != nil {
					return e
				}
			}
		}
	}
}
