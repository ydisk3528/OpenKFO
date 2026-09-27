package persistence

import "testing"

func TestNoticeValidation(t *testing.T) {
	for _, s := range []string{"", "   ", "x\x00y", string(make([]byte, 200))} {
		if validateNotice(s) == nil {
			t.Fatal("accepted invalid notice")
		}
	}
	if err := validateNotice("【线下测试】这是一条普通通知，不会断开游戏。"); err != nil {
		t.Fatal(err)
	}
}
