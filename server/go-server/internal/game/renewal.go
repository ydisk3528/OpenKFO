package game

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"time"

	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
)

type renewalQuote struct {
	Operation string
	Expires   time.Time
	Offers    map[uint32]persistence.RenewalQuote
}

func (h *Hub) renewalReminders(s *Session, p []byte) error {
	if len(p) != 0 {
		s.sendGame(notice("续费列表请求格式不正确。"))
		return nil
	}
	rows, err := storage2_1(h, h.Store.ShopManager().RenewalReminders, s.UID)
	if err != nil {
		s.sendGame(notice("续费列表读取失败，请稍后重试。"))
		return nil
	}
	var data []byte
	for _, r := range rows {
		data = append(data, r.Raw[:]...)
	}
	s.sendGame(protocol.Message{ID: 1410, Payload: data})
	return nil
}

func (h *Hub) ignoreRenewalReminder(s *Session, p []byte) error {
	if len(p) != 4 {
		s.sendGame(notice("忽略续费提醒请求格式不正确。"))
		return nil
	}
	if err := storage1_2(h, h.Store.ShopManager().IgnoreRenewalReminder, s.UID, protocol.ReadUint32(p, 0)); err != nil {
		s.sendGame(notice("忽略提醒失败，请刷新续费列表。"))
		return nil
	}
	s.sendGame(protocol.Message{ID: 1450, Payload: bytes.Clone(p)})
	return nil
}

func (h *Hub) renewalPrices(s *Session, p []byte) error {
	s.RenewalQuote = nil
	quotes, err := storage2_2(h, h.Store.ShopManager().RenewalQuotes, p[0], protocol.ReadUint32(p, 1))
	if err != nil {
		s.sendGame(notice("续费价目读取失败，请稍后重试。"))
		return nil
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	q := &renewalQuote{Operation: hex.EncodeToString(nonce[:]), Expires: time.Now().Add(10 * time.Minute), Offers: map[uint32]persistence.RenewalQuote{}}
	var records []byte
	for _, offer := range quotes {
		q.Offers[protocol.ReadUint32(offer.Catalog[:], 9)] = offer
		records = append(records, offer.Catalog[:]...)
	}
	s.sendGame(protocol.Message{ID: 1510, Payload: records})
	s.RenewalQuote = q
	return nil
}

func (h *Hub) renewItem(s *Session, p []byte) error {
	fail := func() { s.sendGame(protocol.Message{ID: protocol.MsgRenewItemResult, Payload: []byte{0, 0, 0, 0, 0}}) }
	r, err := protocol.ParseRenewalRequest(p)
	q := s.RenewalQuote
	if err != nil || q == nil || time.Now().After(q.Expires) {
		fail()
		return nil
	}
	offer, ok := q.Offers[r.CatalogKey()]
	if !ok {
		fail()
		return nil
	}
	out, err := storage2_4(h, h.Store.ShopManager().RenewItem, s.UID, q.Operation, p, offer)
	if err != nil {
		fail()
		return nil
	}
	if len(out.Item) != 0 {
		s.sendGame(protocol.Message{ID: 2161, Payload: bytes.Clone(out.Item)})
		if s.Inventory == nil {
			s.Inventory = map[uint32][]byte{}
		}
		s.Inventory[r.InventoryInstance()] = bytes.Clone(out.Item)
	}
	s.sendGame(protocol.Message{ID: 1230, Payload: protocol.Uint32Bytes(out.Tickets)})
	ack := append([]byte{1}, protocol.Uint32Bytes(r.InventoryInstance())...)
	s.sendGame(protocol.Message{ID: protocol.MsgRenewItemResult, Payload: ack})
	// Retain this operation through duplicate confirms; a new price query
	// starts a new operation. The native request contains no transaction token.
	return nil
}
