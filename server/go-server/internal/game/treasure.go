package game

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"log"
	"time"
)

// Native handlers verified in gfxz.dat: 19110 -> 827480, 19160 -> 8275B0.
// Experimental local integration: groups and prices are server-owned.
func (h *Hub) treasureMessage(s *Session, m protocol.Message) error {
	if !h.Config.ExperimentalTreasure {
		s.sendGame(notice("百宝暂未开放。"))
		return nil
	}
	if m.ID == 19120 {
		if len(m.Payload) == 0 {
			return nil
		} // Native close notification, never a draw.
		return h.drawTreasure(s, m.Payload)
	}

	if len(m.Payload) != 4 || protocol.ReadUint32(m.Payload, 0) > 1 {
		return protocol.ErrFrame
	}
	if m.ID == 19150 {
		s.sendGame(protocol.Message{ID: 19160, Payload: []byte{}})
		return nil
	}
	if time.Since(s.TreasurePreviewAt) < 500*time.Millisecond {
		return nil
	}
	s.TreasurePreviewAt = time.Now()
	kind := uint8(75 + protocol.ReadUint32(m.Payload, 0))
	settings, err := storage2_0(h, h.Store.TreasureSettings)
	if err != nil {
		s.sendGame(notice("百宝奖池读取失败，请联系管理员。"))
		log.Printf("treasure_config_failed uid=%d error=%v", s.UID, err)
		return nil
	}
	var pool *persistence.TreasurePool
	for i := range settings.Pools {
		if settings.Pools[i].TicketKind == kind {
			pool = &settings.Pools[i]
			break
		}
	}
	if pool == nil {
		s.sendGame(notice("百宝总奖池尚未配置。"))
		return nil
	}
	six, err := pool.RefreshSix()
	if err != nil {
		s.sendGame(notice(err.Error()))
		return nil
	}
	defs, err := storage2_0(h, h.Store.ItemManager().Definitions)
	if err != nil {
		return err
	}
	s.TreasureSerial++
	if s.TreasureSerial == 0 || s.TreasureSerial > 0x7fffffff {
		s.TreasureSerial = 1
	}
	data, err := encodeTreasurePreview(six, defs, s.TreasureSerial, pool.Cost)
	if err != nil {
		s.sendGame(notice(err.Error()))
		return nil
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	selectedDefs := []persistence.ItemDefinition{}
	seenDefs := map[uint32]bool{}
	for _, prize := range six {
		for _, key := range prize.Items {
			for _, d := range defs {
				if d.Key == key && !seenDefs[key] {
					seenDefs[key] = true
					selectedDefs = append(selectedDefs, d)
					break
				}
			}
		}
	}
	data[1] = byte(persistence.TreasureDrawLimit(kind))
	if len(selectedDefs) > 0 {
		catalog, err := storage2_1(h, h.Store.RewardManager().RewardCatalog, selectedDefs)
		if err != nil {
			s.sendGame(notice("百宝道具目录配置冲突，请联系管理员。"))
			return nil
		}
		s.sendGame(protocol.Message{ID: 1550, Payload: catalog})
	}
	s.TreasureDraw = &treasureDrawState{Input: persistence.TreasureDrawInput{Operation: hex.EncodeToString(nonce[:]), Kind: kind, Cost: pool.Cost, Group: six, Definitions: selectedDefs}, Preview: bytes.Clone(data), Cards: bytes.Clone(data[3:213]), Replies: map[string][]byte{}}
	s.sendGame(protocol.Message{ID: 19110, Payload: data})
	log.Printf("treasure_preview uid=%d kind=%d serial=%d count=6 config_revision=%d", s.UID, kind, s.TreasureSerial, settings.Revision)
	return nil
}
func encodeTreasurePreview(six []persistence.TreasurePrize, defs []persistence.ItemDefinition, serial, cost uint32) ([]byte, error) {
	if len(six) != 6 || serial == 0 || cost == 0 || cost > 999 {
		return nil, fmt.Errorf("百宝六格或消耗配置无效")
	}
	p := make([]byte, 271)
	p[0] = 1
	p[1] = 6 // native group tag and maximum draw count
	for i, v := range six {
		count := len(v.Items)
		if v.Gold > 0 {
			count++
		}
		if v.Tickets > 0 {
			count++
		}
		if count != 1 {
			return nil, fmt.Errorf("百宝第%d格需配置单种奖励，道具、金币、点券请分别建条目", i+1)
		}
		b := p[3+i*35 : 3+(i+1)*35]
		b[0] = byte(i)
		binary.LittleEndian.PutUint32(b[1:], uint32(i+1))
		if v.Gold > 0 {
			binary.LittleEndian.PutUint32(b[14:], 4)
			binary.LittleEndian.PutUint32(b[18:], v.Gold)
		}
		if v.Tickets > 0 {
			binary.LittleEndian.PutUint32(b[14:], 2)
			binary.LittleEndian.PutUint32(b[18:], v.Tickets)
		}
		if len(v.Items) == 1 {
			var record []byte
			for _, d := range defs {
				if d.Key == v.Items[0] {
					record = d.Record
					break
				}
			}
			if len(record) != 68 {
				return nil, fmt.Errorf("百宝道具规格不存在")
			}
			binary.LittleEndian.PutUint32(b[14:], 1)
			protocol.WriteUint32(b, 18, v.Items[0])
			// Formatter A1EDF0 reads duration hours at +22 and stack count at +26.
			copy(b[22:26], record[13:17])
			if protocol.ReadUint32(record, 13) == 0 {
				binary.LittleEndian.PutUint32(b[26:], uint32(protocol.ReadUint16(record, 23)))
			}
		}
	}
	for i := 0; i < 6; i++ {
		p[213+i*9] = byte(i + 1)
		binary.LittleEndian.PutUint32(p[213+i*9+5:], cost*uint32(i+1))
	}
	binary.LittleEndian.PutUint32(p[267:], serial)
	return p, nil
}

type treasureDrawState struct {
	Input   persistence.TreasureDrawInput
	Preview []byte
	Cards   []byte
	Replies map[string][]byte
}

func validTreasureDrawRequest(p, preview []byte) bool {
	if len(p) != 30 || len(preview) != 271 || p[0] != preview[0] || p[1] >= 6 || protocol.ReadUint32(p, 26) != protocol.ReadUint32(preview, 267) {
		return false
	}
	seen := map[uint32]bool{}
	for i := 0; i < 6; i++ {
		id := protocol.ReadUint32(p, 2+4*i)
		if id < 1 || id > 6 || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func treasureResultPacket(preview, request []byte, winner int) []byte {
	result := make([]byte, 258)
	// Restore the client's shuffled order, then put the weighted winner under
	// the clicked card. The selection chooses a card, never the actual prize.
	cards := make([]byte, 210)
	winPosition := 0
	for i := 0; i < 6; i++ {
		id := int(protocol.ReadUint32(request, 2+4*i)) - 1
		copy(cards[i*35:(i+1)*35], preview[3+id*35:3+(id+1)*35])
		cards[i*35] = byte(i)
		if id == winner {
			winPosition = i
		}
	}
	selected := int(request[1])
	tmp := bytes.Clone(cards[selected*35 : (selected+1)*35])
	copy(cards[selected*35:(selected+1)*35], cards[winPosition*35:(winPosition+1)*35])
	copy(cards[winPosition*35:(winPosition+1)*35], tmp)
	for i := 0; i < 6; i++ {
		cards[i*35] = byte(i)
		protocol.WriteUint32(cards, i*35+1, protocol.ReadUint32(request, 2+i*4))
		cards[i*35+5] = 0
	}
	cards[selected*35+5] = 1
	copy(result, cards[selected*35:(selected+1)*35])
	protocol.WriteUint32(result, 31, protocol.ReadUint32(preview, 267))
	copy(result[44:254], cards)
	return result
}
func (h *Hub) drawTreasure(s *Session, p []byte) error {
	state := s.TreasureDraw
	if state == nil || !validTreasureDrawRequest(p, state.Preview) {
		s.sendGame(notice("百宝奖组已变化，请刷新后再抽；未扣券。"))
		return nil
	}
	in := state.Input
	in.Request = bytes.Clone(p)
	out, err := storage2_2(h, h.Store.DrawTreasure, s.UID, in)
	if err != nil {
		log.Printf("treasure_draw_failed uid=%d operation=%s error=%v", s.UID, in.Operation, err)
		s.sendGame(notice("百宝抽奖未完成：" + err.Error() + "。请关闭后重新打开百宝。"))
		return nil
	}
	// Re-read current assets even on replay: never restore an old inventory or balance.
	account, err := storage2_1(h, h.Store.RoleManager().Snapshot, s.UID)
	if err != nil {
		s.sendGame(notice("抽奖已结算，背包刷新失败，请重新登录核对；不会重复扣券。"))
		return nil
	}
	s.syncInventory(account.Inventory)
	s.sendGame(protocol.Message{ID: 1230, Payload: protocol.Uint32Bytes(account.Tickets)})
	s.sendGame(protocol.Message{ID: 1240, Payload: protocol.Uint32Bytes(account.Gold)})
	result, err := state.result(p, out)
	if err != nil {
		log.Printf("treasure_display_failed uid=%d error=%v", s.UID, err)
		s.sendGame(notice("奖品已到账，显示校验失败，请重新打开百宝核对。"))
		return nil
	}

	s.sendGame(protocol.Message{ID: 19130, Payload: result})
	log.Printf("treasure_draw uid=%d operation=%s winner=%d replay=%t", s.UID, in.Operation, out.Winner, out.Replay)
	return nil
}

func (state *treasureDrawState) result(p []byte, out persistence.TreasureDrawResult) ([]byte, error) {
	key := string(p)
	result := state.Replies[key]
	if result == nil {
		// Current cards retain stable identities while hidden prize contents move.
		current := bytes.Clone(state.Preview)
		copy(current[3:213], state.Cards)
		target := state.Preview[3+out.Winner*35 : 3+(out.Winner+1)*35]
		winnerIndex := -1
		for i := 0; i < 6; i++ {
			if bytes.Equal(state.Cards[i*35+14:i*35+30], target[14:30]) && out.OpenedMask&(1<<i) == 0 {
				winnerIndex = i
				break
			}
		}
		// The newly opened card may itself already contain the winning prize.
		selectedID := int(protocol.ReadUint32(p, 2+4*int(p[1]))) - 1
		if winnerIndex < 0 && bytes.Equal(state.Cards[selectedID*35+14:selectedID*35+30], target[14:30]) {
			winnerIndex = selectedID
		}
		if winnerIndex < 0 {
			return nil, fmt.Errorf("中奖项目不在当前未翻开的六格中")
		}
		result = treasureResultPacket(current, p, winnerIndex)
		for i := 0; i < 6; i++ {
			r := result[44+i*35 : 44+(i+1)*35]
			id := protocol.ReadUint32(r, 1)
			if out.OpenedMask&(1<<(id-1)) != 0 {
				r[5] = 1
			}
			copy(state.Cards[int(id-1)*35:int(id)*35], r)
		}
		state.Replies[key] = bytes.Clone(result)
	}
	return result, nil
}
