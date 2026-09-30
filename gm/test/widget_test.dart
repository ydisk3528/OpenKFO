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
    expect(calls, ['management_connection_get', 'gm_version']);
    expect(find.textContaining('版本不符合'), findsOneWidget);
  });
  testWidgets('catalog is lazy and management entries have one destination', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1440, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final calls = <String>[];
    await tester.pumpWidget(
      ItemManager(
        api: (r) async {
          calls.add(r['operation'] as String);
          switch (r['operation']) {
            case 'gm_version':
              return {'version': '1.1.0'};
            case 'catalog':
              return {
                'root': 'X:/fixture',
                'items': List.generate(
                  1000,
                  (i) => {
                    'key': '25:$i',
                    'kind': 25,
                    'id': i,
                    'name': '测试武器$i',
                    'group': '武器',
                    'category': '武器',
                    'gender': '通用',
                    'description': '测试',
                    'fields': ['25'],
                    'supported': true,
                  },
                ),
              };
            case 'accounts':
              return [
                {'uid': 1, 'account': 'test001', 'nickname': '测试玩家'},
              ];
            case 'inventory':
              return [];
            case 'shop_images':
              return {};
          }
          return {};
        },
      ),
    );
    await tester.pumpAndSettle();
    expect(calls.contains('accounts'), isFalse);
    expect(calls.contains('inventory'), isFalse);
    expect(find.text('发放到角色'), findsNothing);
    expect(find.text('用户管理'), findsNothing);
    expect(find.text('点券设置与赠送'), findsNothing);
    expect(find.textContaining('测试武器').evaluate().length, lessThan(60));
    await tester.tap(find.text('玩家管理'));
    await tester.pumpAndSettle();
    expect(find.text('批量发道具 / 点券').hitTestable(), findsOneWidget);
    expect(find.text('点券余额设置').hitTestable(), findsOneWidget);
    expect(find.text('普通通知').hitTestable(), findsNothing);
    await tester.tap(find.text('玩家管理'));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('系统管理'));
    await tester.tap(find.text('系统管理'));
    await tester.pumpAndSettle();
    expect(find.text('普通通知').hitTestable(), findsOneWidget);
    expect(find.text('GM 服务器连接').hitTestable(), findsOneWidget);
    await tester.tap(find.text('GM 服务器连接'));
    await tester.pumpAndSettle();
    await tester.enterText(
      find.widgetWithText(TextFormField, 'HTTPS 管理接口'),
      'https://vxziouwkf.top/gm/api',
    );
    await tester.enterText(find.widgetWithText(TextFormField, 'GM 账号'), 'root');
    await tester.enterText(
      find.widgetWithText(TextFormField, '密码'),
      'test-password',
    );
    await tester.tap(find.text('登录'));
    await tester.pumpAndSettle();
    expect(calls.contains('management_connection_login'), isTrue);
    await tester.tap(find.text('选择玩家'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('测试玩家 · test001'));
    await tester.pumpAndSettle();
    expect(calls.where((x) => x == 'accounts').length, 1);
    expect(calls.where((x) => x == 'inventory').length, 1);
    expect(tester.takeException(), isNull);
  });
}
