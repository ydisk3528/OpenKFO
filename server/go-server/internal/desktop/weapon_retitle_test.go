package desktop

import "testing"

func TestRetitleBlockWhitespace(t *testing.T) {
	for _, id := range []string{`"53"`, `"53 "`, `53`} {
		source := `<AnmDesc id=` + id + ` ><Anm id="1" name="500153" /></AnmDesc>`
		got := retitleBlock(source, 51)
		n, err := parseXML(got)
		if err != nil || n.get("id") != "51" || n.children[0].get("name") != "500153" {
			t.Fatalf("retitle %q: %s, %v", id, got, err)
		}
	}
}
