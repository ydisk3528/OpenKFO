package persistence

import (
	"bytes"
	"database/sql"
	"kungfu.local/server/internal/protocol"
	"strings"
	"unicode"
)

// CharacterChoice is one verified 1125 record. Choice and Item are distinct:
// the client submits the server choice, not an arbitrary inventory item ID.
type CharacterChoice struct {
	Gender uint32 `json:"gender"`
	Slot   uint32 `json:"slot"`
	Choice uint32 `json:"choice"`
	Item   uint32 `json:"item"`
}

type CharacterCreation struct {
	Nickname  string
	Request   []byte
	Profile   []byte
	Inventory [][]byte
}

func CharacterOptions(choices []CharacterChoice) ([]byte, error) {
	if len(choices) == 0 || len(choices) > 512 {
		return nil, ErrDenied
	}
	seen := map[[3]uint32]bool{}
	out := make([]byte, 0, len(choices)*16)
	for _, c := range choices {
		key := [3]uint32{c.Gender, c.Slot, c.Choice}
		if (c.Gender != 1 && c.Gender != 2) || c.Slot >= 7 || c.Choice == 0 || c.Item == 0 || seen[key] {
			return nil, ErrDenied
		}
		seen[key] = true
		for _, value := range []uint32{c.Gender, c.Slot, c.Choice, c.Item} {
			out = append(out, protocol.Uint32Bytes(value)...)
		}
	}
	return out, nil
}

// 1150/68 and 1125/16*N verified by nixiang/role-create. Unknown tail bytes
// remain in Request for audit, never interpreted as permissions or item grants.
func ParseCharacterCreation(payload []byte, choices []CharacterChoice) (CharacterCreation, error) {
	var result CharacterCreation
	if len(payload) != 68 {
		return result, ErrDenied
	}
	if _, err := CharacterOptions(choices); err != nil {
		return result, err
	}
	end := bytes.IndexByte(payload[:21], 0)
	if end < 1 || end > 20 || (payload[21] != 1 && payload[21] != 2) || payload[22] >= 20 {
		return result, ErrDenied
	}
	name, err := DecodeGBK(payload[:end])
	if err != nil || !bytes.Equal(GBK(name), payload[:end]) || strings.TrimSpace(name) != name {
		return result, ErrDenied
	}
	for _, c := range name {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) || c == '\ufffd' {
			return result, ErrDenied
		}
	}
	result.Nickname = name
	result.Request = bytes.Clone(payload)
	result.Profile = make([]byte, 360)
	protocol.WriteUint32(result.Profile, 0, 1) // current one-character account model
	copy(result.Profile[4:25], payload[:end])
	result.Profile[122], result.Profile[124] = payload[21], payload[22]
	types := [7]byte{15, 13, 12, 17, 16, 14, 25}
	for slot := uint32(0); slot < 7; slot++ {
		choice := protocol.ReadUint32(payload, 23+int(slot)*4)
		var item uint32
		for _, c := range choices {
			if c.Gender == uint32(payload[21]) && c.Slot == slot && c.Choice == choice {
				item = c.Item
				break
			}
		}
		if item == 0 {
			return CharacterCreation{}, ErrDenied
		}
		record := make([]byte, 68)
		protocol.WriteUint32(record, 0, 0x100000+slot)
		record[4] = types[slot]
		protocol.WriteUint32(record, 5, item)
		protocol.WriteUint16(record, 17, uint16(slot+2))
		// Preserve the legacy weapon count; clothing uses lifetime visibility,
		// because count > 0 selects Use instead of Equip in the native menu.
		protocol.WriteUint16(record, 23, 1)
		normalizeClothingMenu(record)
		result.Inventory = append(result.Inventory, record)
	}
	return result, nil
}

// CreateCharacter never replaces a pre-existing character. The account row lock
// makes repeated requests and competing creates for one account atomic.
func (m *RoleManager) CreateCharacter(uid uint64, payload []byte, choices []CharacterChoice) (Account, error) {
	role, err := ParseCharacterCreation(payload, choices)
	if err != nil {
		return Account{}, err
	}
	if err := m.store.CheckText(role.Nickname); err != nil {
		return Account{}, err
	}
	tx, txCancel, err := beginTransaction(m.store.DB)
	defer txCancel()
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()
	var profile, previous []byte
	if err = tx.QueryRow("SELECT profile FROM accounts WHERE uid=? FOR UPDATE", uid).Scan(&profile); err != nil {
		return Account{}, err
	}
	err = tx.QueryRow("SELECT request FROM character_creations WHERE uid=?", uid).Scan(&previous)
	if err == nil {
		if !bytes.Equal(previous, payload) {
			return Account{}, ErrDenied
		}
		if err = tx.Commit(); err != nil {
			return Account{}, err
		}
		return m.store.RoleManager().Snapshot(uid)
	}
	if err != sql.ErrNoRows {
		return Account{}, err
	}
	if len(profile) != 360 || !bytes.Equal(profile, make([]byte, 360)) {
		return Account{}, ErrDenied
	}
	var count int
	if err = tx.QueryRow("SELECT COUNT(*) FROM inventory WHERE uid=?", uid).Scan(&count); err != nil {
		return Account{}, err
	}
	if count != 0 {
		return Account{}, ErrDenied
	}
	// Preserve names already used by legacy accounts too. New native creates
	// additionally serialize same-name races through the unique nickname index.
	if err = tx.QueryRow("SELECT COUNT(*) FROM accounts WHERE nickname=? AND uid<>?", role.Nickname, uid).Scan(&count); err != nil {
		return Account{}, err
	}
	if count != 0 {
		return Account{}, ErrDenied
	}
	if _, err = tx.Exec("INSERT INTO character_creations(uid,nickname,request) VALUES(?,?,?)", uid, role.Nickname, role.Request); err != nil {
		return Account{}, err
	}
	if _, err = tx.Exec("UPDATE accounts SET nickname=?,profile=? WHERE uid=?", role.Nickname, role.Profile, uid); err != nil {
		return Account{}, err
	}
	for _, record := range role.Inventory {
		if _, err = tx.Exec("INSERT INTO inventory(uid,instance,record) VALUES(?,?,?)", uid, protocol.ReadUint32(record, 0), record); err != nil {
			return Account{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Account{}, err
	}
	return m.store.RoleManager().Snapshot(uid)
}
