import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/weapon_config.dart';

void main() {
  testWidgets('C combo example saves independent effects without applying', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1440, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final calls = <String>[];
    List<dynamic> draft = [];
    Future<dynamic> api(Map<String, dynamic> request) async {
      calls.add(request['operation']);
      if (request['operation'] == 'weapon_catalog') {
        return {
          'revision': 'test-revision',
          'fields': [
            {'key': 'SkillDamage', 'name': '基础伤害', 'min': 0, 'max': 10000},
          ],
          'applied': <String, dynamic>{},
          'drafts': {'253013': draft},
          'buffs': [
            {'id': 0, 'name': '保持原效果'},
            {'id': 1, 'name': '中毒'},
            {'id': 37, 'name': '燃烧（献祭燃烧）'},
          ],
          'weapons': [
            {
              'id': 253013,
              'name': '骤足秘笈',
              'combos': [
                {
                  'name': '站立攻击1',
                  'nodes': [
                    {'keys': 'C', 'state': '1'},
                    {'keys': 'CC', 'state': '2'},
                  ],
                },
              ],
              'stages': List.generate(
                5,
                (i) => {
                  'stage': i + 1,
                  'state': '${i + 1}',
                  'label': i < 2 ? ['C', 'CC'][i] : '跑动普通攻击（动画说明，非按键）',
                  'action': '${2001130 + i}',
                  'property_ids': ['8081$i'],
                  'hits': [
                    {
                      'id': '8081$i',
                      'values': {'SkillDamage': '3'},
                      'buff': '0',
                    },
                  ],
                  'supported': true,
                  'reason': '',
                },
              ),
            },
          ],
        };
      }
      expect(request['operation'], 'weapon_save');
      expect(request['weapon'], 253013);
      expect(request['revision'], 'test-revision');
      draft = (request['rules'] as List)
          .map((r) => Map<String, dynamic>.from(r))
          .toList();
      return {'message': '方案已保存，尚未应用到游戏'};
    }

    await tester.pumpWidget(MaterialApp(home: WeaponConfigPage(api: api)));
    await tester.pumpAndSettle();
    expect(find.text('武器配置'), findsOneWidget);
    expect(find.text('CC'), findsOneWidget);
    await tester.tap(find.text('CC'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('第 1 段 · C'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('填入示例：第一下中毒，第二下燃烧'));
    await tester.pumpAndSettle();
    expect(find.text('DEBUFF'), findsWidgets);
    expect(find.text('受击动作'), findsWidgets);
    expect(find.text('默认（原受击动作）'), findsWidgets);
    expect(find.text('击飞参数 / 高级设置'), findsWidgets);
    expect(
      find.byWidgetPredicate(
        (w) => w is SelectableText && (w.data ?? '').contains('原配置：'),
      ),
      findsNothing,
    );
    expect(find.widgetWithText(TextFormField, '基础伤害'), findsWidgets);
    await tester.enterText(
      find.widgetWithText(TextFormField, '基础伤害').first,
      '7',
    );
    await tester.pumpAndSettle();
    final durations = find.widgetWithText(TextFormField, '持续周期（原生值）');
    await tester.enterText(durations.first, '4500');
    await tester.pumpAndSettle();
    await tester.tap(find.text('保存方案'));
    await tester.pumpAndSettle();
    expect(draft[0]['buff'], 1);
    expect(draft[0]['properties']['80810']['SkillDamage'], 7);
    expect(draft[0]['duration'], 4500);
    expect(draft[1]['buff'], 37);
    expect(draft[2]['buff'], 0);
    expect(calls, isNot(contains('weapon_apply')));
    expect(find.text('方案已保存，尚未应用到游戏'), findsOneWidget);
    expect(find.text('更新到线上'), findsNothing);
    expect(find.text('发布到线上'), findsNothing);
    expect(find.text('导出发版包'), findsOneWidget);
    expect(calls, isNot(contains('weapon_publish')));
    await tester.tap(find.text('动作说明（3）'));
    await tester.pumpAndSettle();
    expect(find.text('跑动普通攻击（动画说明，非按键）'), findsNWidgets(3));
    expect(find.text('状态 3'), findsNothing);
    expect(tester.takeException(), isNull);
  });
}
