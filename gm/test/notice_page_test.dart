import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/notice_page.dart';
void main() {
 testWidgets('notice confirms environment and refreshes the same request', (tester) async {
  final calls=<Map<String,dynamic>>[];
  await tester.pumpWidget(MaterialApp(home:NoticePage(environment:'本地测试服',api:(r) async {
    calls.add(r);return {'state':r['operation']=='notice_send'?'pending':'sent','recipients':2};
  })));
  await tester.enterText(find.byType(TextField),'线下测试');
  await tester.tap(find.text('发送普通通知'));await tester.pumpAndSettle();
  expect(calls,isEmpty);expect(find.text('发送到本地测试服'),findsOneWidget);
  await tester.tap(find.text('确认发送'));await tester.pumpAndSettle();
  await tester.tap(find.text('刷新结果'));await tester.pumpAndSettle();
  expect(calls.length,2);expect(calls[0]['id'],calls[1]['id']);
  expect(find.textContaining('2 个在线连接'),findsOneWidget);
  expect(find.text('新通知'),findsOneWidget);
 });
}
