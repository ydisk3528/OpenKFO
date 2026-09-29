package desktop

import "testing"

// The picker is the only thing standing between an author and a number with no
// animation behind it, so every option must stay inside the field's declared
// range and stay unique. A silent duplicate would make one choice unreachable.
func TestHitOptionsAreInRangeAndUnique(t *testing.T) {
	bounds := map[string]field{}
	for _, item := range propertyFields {
		bounds[item.Key] = item
	}
	for key, options := range hitOptions {
		item, ok := bounds[key]
		if !ok {
			t.Fatalf("选项表 %s 不在字段表里", key)
		}
		if len(options) == 0 {
			t.Fatalf("%s 的选项表是空的", key)
		}
		seen := map[int]bool{}
		for _, option := range options {
			if option.Value < item.Min || option.Value > item.Max {
				t.Fatalf("%s 的选项 %d 超出 %d..%d", key, option.Value, item.Min, item.Max)
			}
			if seen[option.Value] {
				t.Fatalf("%s 的选项 %d 重复", key, option.Value)
			}
			seen[option.Value] = true
			if option.Label == "" {
				t.Fatalf("%s 的选项 %d 没有中文名", key, option.Value)
			}
		}
	}
}

// Damage stays a free number box, so every other field must be a picker;
// otherwise the UI would still be asking authors to guess raw integers.
func TestEveryNonDamageFieldHasOptions(t *testing.T) {
	for _, item := range propertyFields {
		damage := item.Key == "SkillDamage" || item.Key == "SkillEnhanceDamage"
		if damage {
			if item.Oneshot {
				t.Fatalf("%s 是伤害字段，不该走下拉框", item.Key)
			}
			continue
		}
		if !item.Oneshot {
			t.Fatalf("%s 不是伤害字段，应当走下拉框", item.Key)
		}
		if len(hitOptions[item.Key]) == 0 {
			t.Fatalf("%s 标记为下拉框却没有选项表", item.Key)
		}
	}
}

// The zero value of every enumerated field must be offered and must read as
// "off": that is what an untouched node carries, so the picker has to show it.
func TestHitOptionsCoverZero(t *testing.T) {
	for key, options := range hitOptions {
		for _, option := range options {
			if option.Value == 0 {
				goto next
			}
		}
		t.Fatalf("%s 的选项表缺少 0（未设置）", key)
	next:
	}
}

func TestFieldOptionLabelFallsBackToEmpty(t *testing.T) {
	if got := fieldOptionLabel("TargetFlurr", 1); got != "标准浮空（最常用）" {
		t.Fatalf("浮空 1 的中文名不对: %q", got)
	}
	if got := fieldOptionLabel("TargetFlurr", 99); got != "" {
		t.Fatalf("未知值不该有名字，得到 %q", got)
	}
	if got := fieldOptionLabel("NotAField", 1); got != "" {
		t.Fatalf("未知字段不该有名字，得到 %q", got)
	}
}
