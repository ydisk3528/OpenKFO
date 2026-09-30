import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/talisman_config.dart';

void main() {
  testWidgets(
    'edit repair validation and disabled save preserve both lists and revision',
    (t) async {
      Map<String, dynamic>? saved;
      final data = {
        'revision': 7,
        'rules': {
          'enabled': true,
          'uses': [
            {'item': 303002, 'active_cost': 100, 'passive_cost': 0},
          ],
          'repairs': [
            {
              'item': 303002,
              'material': 603001,
              'quantity': 2,
              'capacity': 10000,
            },
          ],
        },
      };
      await t.pumpWidget(
        MaterialApp(
          home: TalismanConfigPage(
            environment: '线上服务器',
            api: (r) async {
              if (r['operation'] == 'catalog') return {'items': [{'id': 303002, 'key': '30:303002', 'name': '测试法宝'}]};
              if (r['operation'] == 'shop_images') return {};
              if (r['operation'] == 'talisman_settings_save') {
                saved = r;
                throw Exception('version conflict');
              }
              return data;
            },
          ),
        ),
      );
      await t.pumpAndSettle();
      expect(find.text('宠物／法宝 · 线上服务器'), findsOneWidget);
      await t.tap(find.text('材料修理'));
      await t.pumpAndSettle();
      await t.tap(find.text('测试法宝 · 303002'));
      await t.pumpAndSettle();
      await t.enterText(find.byKey(const ValueKey('material')), '303002');
      await t.tap(find.text('确定'));
      await t.pumpAndSettle();
      expect(find.text('修理材料不能是法宝自身'), findsOneWidget);
      await t.enterText(find.byKey(const ValueKey('material')), '603001');
      await t.enterText(find.byKey(const ValueKey('capacity')), '65536');
      await t.tap(find.text('确定'));
      await t.pumpAndSettle();
      expect(find.text('修复后额度原值无效'), findsOneWidget);
      await t.enterText(find.byKey(const ValueKey('capacity')), '20000');
      await t.tap(find.text('确定'));
      await t.pumpAndSettle();
      expect(t.takeException(), isNull);
      expect(saved, isNull);
      await t.tap(find.text('启用宠物／法宝规则'));
      await t.pumpAndSettle();
      await t.tap(find.text('保存法宝配置'));
      await t.pumpAndSettle();
      final c = saved!['talisman_settings'];
      expect(c['revision'], 7);
      expect(c['rules']['enabled'], false);
      expect(c['rules']['uses'][0]['active_cost'], 100);
      expect(c['rules']['repairs'][0]['capacity'], 20000);
      expect(find.textContaining('version conflict'), findsOneWidget);
      expect(find.textContaining('容量 20000'), findsOneWidget);
    },
  );
  testWidgets('read failure disables save', (t) async {
    await t.pumpWidget(
      MaterialApp(
        home: TalismanConfigPage(
          environment: '本地测试服',
          api: (_) async => throw Exception('offline'),
        ),
      ),
    );
    await t.pumpAndSettle();
    expect(t.widget<FilledButton>(find.byType(FilledButton)).onPressed, isNull);
  });
  testWidgets('duplicate and empty enable rejected before save', (t) async {
    var saves = 0;
    await t.pumpWidget(
      MaterialApp(
        home: TalismanConfigPage(
          environment: '本地测试服',
          api: (r) async {
            if (r['operation'] == 'talisman_settings_save') saves++;
            return {
              'revision': 0,
              'rules': {
                'enabled': false,
                'uses': [
                  {'item': 303002, 'active_cost': 0, 'passive_cost': 0},
                ],
                'repairs': [],
              },
            };
          },
        ),
      ),
    );
    await t.pumpAndSettle();
    await t.tap(find.text('新增规则'));
    await t.pumpAndSettle();
    await t.enterText(find.byKey(const ValueKey('item')), '303002');
    await t.enterText(find.byKey(const ValueKey('active_cost')), '0');
    await t.enterText(find.byKey(const ValueKey('passive_cost')), '0');
    await t.tap(find.text('确定'));
    await t.pumpAndSettle();
    expect(find.text('同类规则物品ID重复'), findsOneWidget);
    await t.tap(find.text('取消'));
    await t.pumpAndSettle();
    await t.tap(find.byTooltip('删除规则'));
    await t.pumpAndSettle();
    await t.tap(find.text('启用宠物／法宝规则'));
    await t.pumpAndSettle();
    await t.tap(find.text('保存法宝配置'));
    await t.pumpAndSettle();
    expect(saves, 0);
    expect(find.textContaining('启用至少需要一条规则'), findsOneWidget);
  });
}
