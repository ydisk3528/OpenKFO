package desktop

import "testing"

func TestWeaponExchangeCardSale(t *testing.T) {
	if !quantityItem(60, 603302) || quantityItem(25, 603302) || quantityItem(60, 603301) || stageTicket(60, 603302) {
		t.Fatal("exchange card classification leaked to other materials or stage tickets")
	}
	enabled := true
	item := Item{ID: 603302, Kind: 60, Stackable: quantityItem(60, 603302)}
	o, err := offer(item, Request{Enabled: &enabled, Currency: "ticket", Price: 30, Days: 365, Quantity: 1})
	if err != nil {
		t.Fatal(err)
	}
	if o.Category != 10 || o.Variant != 60 || little.Uint32(o.Record[38:]) != 30 || little.Uint32(o.Record[26:]) != 1 || little.Uint16(o.Grant[23:]) != 1 || little.Uint32(o.Grant[13:]) != 0 || little.Uint16(o.Grant[17:]) != 0 {
		t.Fatal("exchange card must be sold as one unequipped, non-expiring material")
	}
}
