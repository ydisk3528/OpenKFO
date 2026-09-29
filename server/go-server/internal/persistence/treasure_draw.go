package persistence

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"kungfu.local/server/internal/protocol"
	"math"
	"math/big"
	"time"
)

// TreasureDrawInput is a server-owned snapshot of the displayed group.
type TreasureDrawInput struct {
	Operation   string
	Kind        byte
	Cost        uint32
	Group       []TreasurePrize
	Definitions []ItemDefinition
	Request     []byte
}
type TreasureDrawResult struct {
	Winner        int
	WonMask       uint32
	OpenedMask    uint32
	Gold, Tickets uint32
	Replay        bool
}

func (s *Store) DrawTreasure(uid uint64, in TreasureDrawInput) (out TreasureDrawResult, err error) {
	if uid == 0 || len(in.Operation) == 0 || len(in.Operation) > 80 || (in.Kind != 75 && in.Kind != 76) || in.Cost < 1 || in.Cost > 999 || len(in.Group) != 6 || len(in.Request) != 30 {
		return out, ErrDenied
	}
	for _, p := range in.Group {
		if p.Weight < 1 || p.Weight > 1000000 || p.Validate() != nil {
			return out, ErrDenied
		}
	}
	tx, cancel, err := beginTransaction(s.DB)
	defer cancel()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRow("SELECT gold,tickets FROM accounts WHERE uid=? FOR UPDATE", uid).Scan(&out.Gold, &out.Tickets); err != nil {
		return out, err
	}
	if in.Request[1] >= 6 {
		return out, ErrDenied
	}
	cardID := protocol.ReadUint32(in.Request, 2+4*int(in.Request[1]))
	if in.Request[1] >= 6 || cardID < 1 || cardID > 6 {
		return out, ErrDenied
	}
	operation := fmt.Sprintf("%s:%d", in.Operation, cardID)
	hash := sha256.Sum256(in.Request)
	var previousHash, raw []byte
	err = tx.QueryRow("SELECT request_hash,result FROM treasure_draws WHERE uid=? AND operation_id=?", uid, operation).Scan(&previousHash, &raw)
	if err == nil {
		if !bytes.Equal(hash[:], previousHash) {
			return out, fmt.Errorf("此卡已翻开，请选择未翻开的卡")
		}
		if err = json.Unmarshal(raw, &out); err != nil {
			return out, err
		}
		out.Replay = true
		return out, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return out, err
	}
	prior, err := tx.Query("SELECT result FROM treasure_draws WHERE uid=? AND LEFT(operation_id,?)=?", uid, len(in.Operation)+1, in.Operation+":")
	if err != nil {
		return out, err
	}
	draws := uint32(0)
	for prior.Next() {
		var b []byte
		if err = prior.Scan(&b); err != nil {
			prior.Close()
			return out, err
		}
		var r TreasureDrawResult
		if err = json.Unmarshal(b, &r); err != nil {
			prior.Close()
			return out, err
		}
		out.WonMask |= r.WonMask
		out.OpenedMask |= r.OpenedMask
		draws++
	}
	err = prior.Err()
	prior.Close()
	if err != nil {
		return out, err
	}
	if draws >= TreasureDrawLimit(in.Kind) || out.OpenedMask&(1<<(cardID-1)) != 0 {
		return out, fmt.Errorf("此组已翻完或此卡已翻开，请刷新百宝")
	}
	cost := in.Cost * (draws + 1)
	rows, err := tx.Query(`SELECT i.instance,i.record FROM inventory i LEFT JOIN inventory_expirations e ON e.uid=i.uid AND e.instance=i.instance WHERE i.uid=? AND (e.instance IS NULL OR e.expires_at>?) ORDER BY i.instance FOR UPDATE`, uid, time.Now().Unix())
	if err != nil {
		return out, err
	}
	var cards [][]byte
	var count uint64
	for rows.Next() {
		var id uint32
		var r []byte
		if err = rows.Scan(&id, &r); err != nil {
			rows.Close()
			return out, err
		}
		if len(r) == 68 && r[4] == in.Kind && usableItem(r) && protocol.ReadUint16(r, 17) == 0 {
			cards = append(cards, r)
			count += uint64(protocol.ReadUint16(r, 23))
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if count < uint64(cost) {
		return out, fmt.Errorf("百宝券不足：需要%d张，当前%d张", cost, count)
	}
	remaining := cost
	for _, r := range cards {
		if remaining == 0 {
			break
		}
		n := uint32(protocol.ReadUint16(r, 23))
		take := min(n, remaining)
		remaining -= take
		if n == take {
			if _, err = tx.Exec("DELETE FROM inventory_expirations WHERE uid=? AND instance=?", uid, protocol.ReadUint32(r, 0)); err != nil {
				return out, err
			}
			_, err = tx.Exec("DELETE FROM inventory WHERE uid=? AND instance=?", uid, protocol.ReadUint32(r, 0))
		} else {
			protocol.WriteUint16(r, 23, uint16(n-take))
			_, err = tx.Exec("UPDATE inventory SET record=? WHERE uid=? AND instance=?", r, uid, protocol.ReadUint32(r, 0))
		}
		if err != nil {
			return out, err
		}
	}
	var total uint64
	for i, p := range in.Group {
		if out.WonMask&(1<<i) == 0 {
			total += uint64(p.Weight)
		}
	}
	if total == 0 {
		return out, ErrDenied
	}
	ticket, e := rand.Int(rand.Reader, new(big.Int).SetUint64(total))
	if e != nil {
		return out, e
	}
	n := ticket.Uint64()
	for i, p := range in.Group {
		if out.WonMask&(1<<i) != 0 {
			continue
		}
		if n < uint64(p.Weight) {
			out.Winner = i
			break
		}
		n -= uint64(p.Weight)
	}
	out.WonMask |= 1 << out.Winner
	out.OpenedMask |= 1 << (cardID - 1)

	prize := in.Group[out.Winner]
	if uint64(out.Gold)+uint64(prize.Gold) > math.MaxInt32 || uint64(out.Tickets)+uint64(prize.Tickets) > math.MaxInt32 {
		return out, fmt.Errorf("余额达到上限，未扣券")
	}
	for _, key := range prize.Items {
		var d *ItemDefinition
		for i := range in.Definitions {
			if in.Definitions[i].Key == key {
				d = &in.Definitions[i]
				break
			}
		}
		if d == nil || !validDefinition(*d) {
			return out, fmt.Errorf("奖品规格无效，未扣券")
		}
		if _, err = (InventoryManager{}).AddItem(tx, uid, d.Record, d.Days); err != nil {
			return out, err
		}
	}
	out.Gold += prize.Gold
	out.Tickets += prize.Tickets
	if _, err = tx.Exec("UPDATE accounts SET gold=?,tickets=? WHERE uid=?", out.Gold, out.Tickets, uid); err != nil {
		return out, err
	}
	snapshot, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	raw, err = json.Marshal(out)
	if err != nil {
		return out, err
	}
	if _, err = tx.Exec("INSERT INTO treasure_draws(uid,operation_id,request_hash,snapshot,result) VALUES(?,?,?,?,?)", uid, operation, hash[:], snapshot, raw); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// Ordinary treasure ends after three flips; festival keeps its six-flip round.
func TreasureDrawLimit(kind byte) uint32 {
	if kind == 75 {
		return 3
	}
	return 6
}
