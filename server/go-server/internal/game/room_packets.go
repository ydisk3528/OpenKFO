package game

import (
	"bytes"

	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

// Native room wire layouts live here; state transitions remain in rooms.go.
// See protocol/RoomProtocol.md for client addresses and verification limits.
func roomEntry(room *Room, uid uint64) []byte {
	request := room.Request
	entry := make([]byte, 245)
	protocol.WriteUint16(entry, 0, room.ID)
	protocol.WriteUint64(entry, 2, uid)
	copy(entry[12:20], request[38:46])
	copy(entry[24:45], request[:21])
	copy(entry[45:56], request[21:32])
	copy(entry[56:61], request[32:37])
	if request[21] != 0 {
		entry[61] = 1
	}
	entry[62] = request[37]
	entry[64] = byte(room.observerLimit())
	entry[65] = request[protocol.RoomTypeOffset]
	copy(entry[67:69], request[47:49])
	copy(entry[69:73], request[50:54])
	entry[73] = request[49]
	if room.Series != nil {
		protocol.WriteUint32(entry, 74, room.Series.limit)
	}
	copy(entry[78:82], request[59:63])
	protocol.WriteUint64(entry, 96, uid)
	return entry
}
func fighter(account persistence.Account, member *Member) []byte {
	record := make([]byte, 149)
	protocol.WriteUint64(record, 0, account.UID)
	record[8] = member.Slot
	record[9] = member.Spawn
	record[10] = member.Team
	copy(record[11:32], persistence.GBK(account.Nickname))
	if member.Ready {
		record[53] = 1
	}
	copy(record[54:57], account.Profile[122:125])
	protocol.WriteUint32(record, 67, member.Session.P2P)
	// Native 3090 handler 81EEA0 routes record[76] != 0 to the spectator
	// container. Preserve this identity on equipment refreshes.
	if member.Spectator {
		record[76] = 1
	}
	for _, item := range account.Inventory {
		if protocol.ReadUint16(item, 17) != 0 {
			record[64]++
			record = append(record, item...)
		}
	}
	return record
}
func roomList(room *Room) []byte {
	entry := roomEntry(room, room.Owner)
	record := make([]byte, 259)
	protocol.WriteUint16(record, 0, room.ID)
	copy(record[2:23], entry[24:45])
	copy(record[23:31], entry[12:20])
	record[31] = entry[61]
	record[33] = entry[57]
	record[39] = entry[62]
	if room.observerLimit() > 0 {
		record[34] = 1
	}
	protocol.WriteUint16(record, 35, uint16(room.observerLimit()))
	protocol.WriteUint16(record, 37, uint16(room.observerCount()))
	record[40] = byte(room.fighterCount())
	if room.Stage == "room" {
		record[41] = 1
	}
	record[42] = entry[59]
	record[43] = entry[60]
	record[44] = entry[65]
	return record
}

// Native 3350/81F270: UID, appearance bytes, item count, cosmetic metadata,
// then 68-byte inventory records. Apply after 3105 so removed slots are cleared.
func roomEquipmentEffects(roster []byte) []byte {
	const headerSize = 21
	items := roster[149:]
	storage := len(items)
	if storage == 0 {
		// 81F36C reads the first item's kind even when the count is zero.
		storage = protocol.InventoryRecordSize
	}
	p := make([]byte, headerSize+storage)
	copy(p[:8], roster[:8])
	p[8], p[9], p[11] = roster[54], roster[56], roster[64]
	copy(p[12:16], roster[136:140])
	copy(p[16:21], roster[144:149])
	copy(p[headerSize:], items)
	return p
}

func roomEntryForMember(room *Room, member *Member, own []byte) []byte {
	entry := roomEntry(room, room.Owner)
	entry[10], entry[11], entry[66] = member.Slot, member.Spawn, member.Team
	// Preserve the existing practice entry path until its model load is verified.
	if room.Type() != protocol.FreePractice || len(room.Members) > 1 {
		copy(entry[96:], own[:149])
	}
	return entry
}

func roomTeam(member *Member) []byte {
	return append(protocol.Uint64Bytes(member.Session.UID), member.Team, member.Spawn)
}

// applyRoomSettings validates the 3200 layout without changing room state.
// Map availability is a separate policy checked by Hub.resolve.
func applyRoomSettings(request, payload []byte) ([]byte, error) {
	if len(request) != 81 || len(payload) != 48 {
		return nil, protocol.ErrFrame
	}
	if payload[8] != 0 || payload[11] != 0 || payload[9] > 1 || payload[10] > 1 || payload[12] > 1 || payload[13] > 1 || payload[14] == 0 {
		return nil, protocol.ErrFrame
	}
	duration := protocol.ReadUint16(payload, 46)
	unlimitedPractice := duration == 0 && protocol.RoomTypeFromRequest(request) == protocol.FreePractice
	if !unlimitedPractice && duration != 120 && duration != 180 && duration != 240 && duration != 300 {
		return nil, protocol.ErrFrame
	}
	for _, field := range [][]byte{payload[14:35], payload[35:46]} {
		terminator := bytes.IndexByte(field, 0)
		if terminator < 0 || !bytes.Equal(field[terminator:], make([]byte, len(field)-terminator)) {
			return nil, protocol.ErrFrame
		}
	}
	candidate := bytes.Clone(request)
	copy(candidate[:21], payload[14:35])
	copy(candidate[21:32], payload[35:46])
	copy(candidate[32:35], payload[8:11])
	copy(candidate[35:37], payload[12:14])
	copy(candidate[38:46], payload[:8])
	copy(candidate[47:49], payload[46:48])
	return candidate, nil
}

func roomSettings(request []byte) []byte {
	payload := make([]byte, 48)
	copy(payload[:8], request[38:46])
	copy(payload[8:11], request[32:35])
	copy(payload[12:14], request[35:37])
	copy(payload[14:35], request[:21])
	copy(payload[35:46], request[21:32])
	copy(payload[46:48], request[47:49])
	return payload
}
