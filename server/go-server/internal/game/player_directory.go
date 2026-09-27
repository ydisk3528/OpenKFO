package game

import (
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"sort"
	"strings"
)

// Native 92DBB0 sends page and capacity; 92DAE0 selects 7 or 10.
// 8243D0 consumes 8+68*n and passes header+4 to the page ceiling setter.
func (hub *Hub) playerPage(lobbyID uint32, payload []byte) ([]uint64, []byte, error) {
	if len(payload) != 8 {
		return nil, nil, protocol.ErrFrame
	}
	page, size := protocol.ReadUint32(payload, 0), protocol.ReadUint32(payload, 4)
	if page == 0 || (size != 7 && size != 10) {
		return nil, nil, protocol.ErrFrame
	}
	var ids []uint64
	for uid, s := range hub.Sessions {
		ch := s.Channels[s.GameChannel]
		if s.LobbyID != lobbyID || s.LoggedOut || s.GameChannel == 0 || ch == nil || (ch.Phase != "lobby" && ch.Phase != "room") {
			continue
		}
		select {
		case <-s.Done:
			continue
		default:
		}
		ids = append(ids, uid)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	pages := uint32((len(ids) + int(size) - 1) / int(size))
	if pages == 0 {
		pages = 1
	}
	if page > pages {
		page = pages
	}
	header := append(protocol.Uint32Bytes(page), protocol.Uint32Bytes(pages)...)
	start := int(page-1) * int(size)
	end := min(start+int(size), len(ids))
	return ids[start:end], header, nil
}

func playerListRecord(uid uint64, name string, profile []byte) ([]byte, error) {
	if uid == 0 || len(profile) != 360 || (profile[122] != 1 && profile[122] != 2) {
		return nil, protocol.ErrFrame
	}
	encoded := persistence.GBK(name)
	decoded, err := persistence.DecodeGBK(encoded)
	if err != nil || decoded != name || len(encoded) == 0 || len(encoded) > 20 || strings.ContainsRune(name, 0) {
		return nil, protocol.ErrFrame
	}
	p := make([]byte, 68)
	protocol.WriteUint64(p, 0, uid)
	copy(p[8:29], encoded)
	p[29] = profile[122]                            // 896F20 -> picPlayerSex
	p[30] = byte(persistence.ProfileLevel(profile)) // 89773E -> anmPlayerLevel
	// Reputation, custom level icon, membership and other unverified optional
	// metadata remain unset. Never derive them from gold or experience.
	return p, nil
}

func (hub *Hub) playerDirectory(s *Session, payload []byte) error {
	ids, reply, err := hub.playerPage(s.LobbyID, payload)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		args := make([]any, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		// Only page summaries: no passwords, inventory or per-player DB round trips.
		reply, err = storage2_0(hub, func() ([]byte, error) {
			rows, err := hub.Store.DB.Query("SELECT uid,nickname,profile FROM accounts WHERE uid IN ("+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+") ORDER BY uid", args...)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			count := 0
			for rows.Next() {
				var uid uint64
				var name string
				var profile []byte
				if err = rows.Scan(&uid, &name, &profile); err != nil {
					return nil, err
				}
				record, err := playerListRecord(uid, name, profile)
				if err != nil {
					return nil, err
				}
				reply = append(reply, record...)
				count++
			}
			if err = rows.Err(); err != nil {
				return nil, err
			}
			if count != len(ids) {
				return nil, persistence.ErrDenied
			}
			return reply, nil
		})
		if err != nil {
			return err
		}
	}
	s.sendGame(protocol.Message{ID: protocol.MsgPlayerList, Payload: reply})
	return nil
}
