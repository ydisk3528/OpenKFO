import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/treasure_config.dart';

void main() {
  testWidgets('saves money pools and refuses empty rewards', (t) async {
    final writes = <Map<String, dynamic>>[];
    await t.pumpWidget(
      MaterialApp(
        home: TreasureConfigPage(
          environment: '线下',
          api: (r) async {
            if (r['operation'] == 'catalog') return {'items': []};
            if (r['operation'] == 'definitions_get') return [];
            if (r['operation'] == 'treasure_get')
              return {
                'revision': 2,
                'pools': [
                  {'name': '百宝', 'ticket_kind': 75, 'cost': 1, 'prizes': []},
                ],
              };
            writes.add(r);
            return {...r['treasure'] as Map, 'revision': 3};
          },
        ),
      ),
    );
    await t.pumpAndSettle();
    await t.tap(find.text('添加奖励'));
    await t.pumpAndSettle();
    await t.tap(find.text('保存百宝配置'));
    await t.pumpAndSettle();
    expect(writes, isEmpty);
    final field = find.widgetWithText(TextFormField, '点券');
    await t.ensureVisible(field);
    await t.enterText(field, '100');
    await t.tap(find.text('保存百宝配置'));
    await t.pumpAndSettle();
    expect(writes.single['treasure']['revision'], 2);
    expect(writes.single['treasure']['pools'][0]['prizes'][0]['tickets'], 100);
  });

  testWidgets('preview uses only the displayed six weights', (t) async {
    final prizes = List.generate(
      7,
      (i) => {
        'weight': i == 0 ? 50 : 10,
        'gold': 0,
        'tickets': i + 1,
        'items': <int>[],
      },
    );
    var previews = 0;
    await t.pumpWidget(
      MaterialApp(
        home: TreasureConfigPage(
          environment: '线下',
          api: (r) async {
            if (r['operation'] == 'catalog') return {'items': []};
            if (r['operation'] == 'definitions_get') return [];
            if (r['operation'] == 'treasure_get')
              return {
                'revision': 1,
                'pools': [
                  {
                    'name': '百宝',
                    'ticket_kind': 75,
                    'cost': 1,
                    'prizes': prizes,
                  },
                ],
              };
            expect(r['operation'], 'treasure_preview');
            previews++;
            expect(r['treasure']['pools'][0]['prizes'].length, 7);
            return prizes.take(6).toList();
          },
        ),
      ),
    );
    await t.pumpAndSettle();
    await t.tap(find.text('刷新六格预览'));
    await t.pumpAndSettle();
    expect(previews, 1);
    expect(find.textContaining('本组中奖率 50.00%'), findsOneWidget);
    expect(find.textContaining('总奖池刷新时随机选出6条'), findsOneWidget);
  });
}
