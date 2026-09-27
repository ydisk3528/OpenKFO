import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/main.dart';

void main() {
  testWidgets('version mismatch blocks initial catalogue and grant', (
    tester,
  ) async {
    final calls = <String>[];
    await tester.pumpWidget(
      ItemManager(
        api: (r) async {
          calls.add(r['operation'] as String);
          return {'version': 'old'};
        },
      ),
    );
    await tester.pumpAndSettle();
    expect(calls, ['gm_version']);
    expect(find.textContaining('版本不符合'), findsOneWidget);
  });
  testWidgets('catalog searches and adds to selected character', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1440, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    Map<String, dynamic>? grant;
    Future<dynamic> api(Map<String, dynamic> r) async {
      switch (r['operation']) {
        case 'gm_version':
          return {'version': '1.1.0'};
        case 'catalog':
          return {
            'root': 'X:/fixture',
            'items': [
              {
                'key': '12:121001',
                'kind': 12,
                'id': 121001,
                'name': '测试上衣',
                'group': '服装外观',
                'category': '上衣',
                'gender': '通用',
                'icon': '',
                'description': '测试物品',
                'fields': ['12', '121001'],
                'supported': true,
                'stackable': false,
              },
            ],
          };
        case 'accounts':
          return [
            {'uid': 1002, 'account': 'localguest', 'nickname': '游客'},
          ];
        case 'inventory':
          return [];
        case 'grant':
          grant = r;
          return {
            'added': 1,
            'updated': 0,
            'skipped': 0,
            'backup': 'fixture-backup',
          };
      }
    }

    await tester.pumpWidget(ItemManager(api: api));
    await tester.pumpAndSettle();
    expect(find.text('测试上衣'), findsOneWidget);
    await tester.enterText(find.byType(TextField).first, 'missing');
    await tester.pumpAndSettle();
    expect(find.text('没有匹配的道具'), findsOneWidget);
    await tester.enterText(find.byType(TextField).first, '121001');
    await tester.pumpAndSettle();
    await tester.tap(find.byType(Checkbox).first);
    await tester.pumpAndSettle();
    await tester.tap(find.text('添加已选 1 件'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('确认添加'));
    await tester.pumpAndSettle();
    expect(grant?['days'], 365);
    expect(grant?['quantity'], 1);
    expect(grant?['uid'], 1002);
    expect(grant?['environment'], 'online');
    expect(grant?['gm_version'], '1.1.0');
    expect(grant?['keys'], ['12:121001']);
    expect(find.text('背包已更新'), findsOneWidget);
    await tester.tap(find.text('知道了'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('玩家管理'));
    await tester.pumpAndSettle();
    expect(find.text('VIP管理').hitTestable(), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}
