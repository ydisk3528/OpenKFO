package desktop

import (
	"strings"
	"testing"
)

func TestAppendComboRowsSkipsDonorWithoutTable(t *testing.T) {
	table := `<ItemList>
	<Item WeaponTypeId ="253013" OldState="2011" NewState="2012" KeyInput="1" StartPart="0"/>
	<Item WeaponTypeId ="253013" OldState="2012" NewState="2013" KeyInput="1" StartPart="0"/>
</ItemList>`

	// 借体没有连招表：不报错，跳过连招复制（特效等其它登记照常）。
	next, rows, err := appendComboRows(table, "253504", "253300")
	if err != nil {
		t.Fatalf("donor without a combo table must not fail: %v", err)
	}
	if rows != 0 {
		t.Fatalf("want 0 copied rows, got %d", rows)
	}
	if next != table {
		t.Fatalf("text must be unchanged when nothing was copied")
	}

	// 借体有连招表：照常复制，目标得到相同条数。
	next, rows, err = appendComboRows(table, "253013", "253300")
	if err != nil {
		t.Fatalf("copy failed: %v", err)
	}
	if rows != 2 {
		t.Fatalf("want 2 copied rows, got %d", rows)
	}
	if !strings.Contains(next, `WeaponTypeId ="253300"`) {
		t.Fatalf("target rows missing:\n%s", next)
	}
}
