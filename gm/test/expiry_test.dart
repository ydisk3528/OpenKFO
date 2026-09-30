import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/main.dart';

void main() {
  testWidgets('deadline validates input and preserves target', (tester) async {
    tester.view.physicalSize = const Size(1440, 1100);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    Map<String, dynamic>? saved;
    Future<dynamic> api(Map<String, dynamic> r) async {
      switch (r['operation']) {
        case 'management_connection_get': return {'endpoint':'https://gm.example/gm/api'};
        case 'gm_version': return {'version':'1.1.0'};
        case 'catalog': return {'root':'X:/fixture','items':[{
          'key':'25:253002','kind':25,'id':253002,'name':'测试武器',
          'group':'武器','category':'武器','gender':'通用','icon':'',
          'description':'测试','fields':['25'],'supported':true,'stackable':false,
        }]};
        case 'accounts': return [{'uid':1002,'account':'test','nickname':'测试'}];
        case 'inventory': return [{'instance':42,'key':'25:253002','slot':0,'quantity':1,
          'duration_state':0,'expires_at':saved?['expires_at'] ?? 0}];
        case 'inventory_expiry': saved=r;return {'message':'期限保存成功'};
      }
      throw StateError('unexpected request');
    }
    await tester.pumpWidget(ItemManager(api:api));await tester.pumpAndSettle();
    await tester.tap(find.text('选择玩家'));await tester.pumpAndSettle();
    await tester.tap(find.text('测试 · test'));await tester.pumpAndSettle();
    await tester.tap(find.text('测试武器'));await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('修改服务器期限'));
    await tester.tap(find.text('修改服务器期限'));await tester.pumpAndSettle();
    final input=find.descendant(of:find.byType(AlertDialog),matching:find.byType(TextField));
    await tester.enterText(input, '0');
    await tester.tap(find.text('保存期限'));await tester.pumpAndSettle();
    expect(saved,isNull);expect(find.text('请输入1–3650的整数'),findsOneWidget);
    await tester.enterText(input, '2');
    await tester.tap(find.text('保存期限'));await tester.pumpAndSettle();
    expect(saved?['uid'],1002);expect(saved?['instance'],42);expect(saved?['environment'],'online');
    final deadline=saved!['expires_at'] as int;
    expect(deadline-DateTime.now().millisecondsSinceEpoch~/1000,inInclusiveRange(172790,172800));
    expect(tester.takeException(),isNull);
  });
}
