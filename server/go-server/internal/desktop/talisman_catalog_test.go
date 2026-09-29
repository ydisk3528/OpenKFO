package desktop

import (
	"kungfu.local/server/internal/protocol"
	"os"
	"testing"
)

func TestTalismanCostPrecision(t *testing.T) {
	for text, want := range map[string]uint16{"0": 0, "0.08": 8, "0.82": 82, "655.35": 65535} {
		got, err := talismanCost(text)
		if err != nil || got != want {
			t.Fatal(text, got, err)
		}
	}
	for _, text := range []string{"", "-1", "0.001", "655.36", "NaN"} {
		if _, err := talismanCost(text); err == nil {
			t.Fatal(text)
		}
	}
}

func TestTalismanShopHasUsableQuota(t *testing.T) {
	item := Item{ID: 303110, Kind: protocol.ItemTalisman, Timed: timed(protocol.ItemTalisman)}
	enabled := true
	o, err := offer(item, Request{Enabled: &enabled, Currency: "ticket", Price: 100, Days: 365, Quantity: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !item.Timed || little.Uint16(o.Grant[23:]) != initialTalismanQuota || little.Uint32(o.Record[26:]) != initialTalismanQuota || little.Uint16(o.Grant[17:]) != protocol.SlotUnequipped {
		t.Fatal("new pet must have quota and enter bag unequipped")
	}
	if little.Uint32(o.Grant[9:]) != little.Uint32(o.Record[9:]) {
		t.Fatal("native maximum durability lookup cannot find shop entry")
	}
}

func TestInstalledTalismanCatalogue(t *testing.T) {
	client := os.Getenv("OPENKFO_TEST_CLIENT")
	if client == "" {
		t.Skip("installed client required")
	}
	items, err := catalog(client, false, false)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := clientTalismanRules(client, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) == 0 {
		t.Fatal("empty rules")
	}
	t.Logf("verified %d pet/talisman rules", len(rules))
}
