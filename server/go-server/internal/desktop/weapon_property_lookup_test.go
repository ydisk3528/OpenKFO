package desktop

import "testing"

// 原生 skillproperty.xml 里两类「不干净」的数据必须被区分开：
//   - 悬空引用（动作块写着 skillproid，表里根本没有）→ 不能编辑；
//   - 重复定义（同一个号写了两遍，内容还不一样）→ 能编辑，按第一条，提醒作者。
//
// 过去 inspect 一律要求「恰好 1 条」，把重复定义这种正常可用的数据也拦成了
// 「动作或命中属性未能唯一对应，暂不可应用」——253032/253033/253138/253151/
// 253511/253917 等 17 个状态因此完全不提供伤害编辑。
func TestPropertyAvailabilitySeparatesMissingFromDuplicated(t *testing.T) {
	once := mustParse(t, `<PropertyItem SkillProId="2009028" SkillDamage="5" />`)
	first := mustParse(t, `<PropertyItem SkillProId="813104" SkillDamage="5" DamageLevel="3" />`)
	second := mustParse(t, `<PropertyItem SkillProId="813104" SkillDamage="5" DamageLevel="2" />`)
	info := &inspection{properties: map[string][]*xmlNode{
		"2009028": {once},
		"813104":  {first, second},
	}}

	missing, duplicated := propertyAvailability(info, []string{"2009028", "813104", "60011780"})
	if len(missing) != 1 || missing[0] != "60011780" {
		t.Fatalf("悬空引用判定错误：missing=%v", missing)
	}
	if len(duplicated) != 1 || duplicated[0] != "813104" {
		t.Fatalf("重复定义判定错误：duplicated=%v", duplicated)
	}

	// 重复定义按第一条生效，和 render 的写法保持一致。
	node, ok := info.propertyNode("813104")
	if !ok {
		t.Fatal("重复定义应当可用（取第一条）")
	}
	if node.get("DamageLevel") != "3" {
		t.Fatalf("重复定义应当取第一条，得到 DamageLevel=%q", node.get("DamageLevel"))
	}
	if _, ok = info.propertyNode("60011780"); ok {
		t.Fatal("悬空引用不该被当成可用")
	}
}
