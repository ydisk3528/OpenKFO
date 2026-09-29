import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/login_error_config.dart';

void main() {
  testWidgets('edits text and resets without changing fixed code', (t) async {
    final writes = <Map<String, dynamic>>[];
    final catalog = [
      {
        'code': 'server_full',
        'default': '默认满员提示',
        'tip': '达到人数限制',
        'editable': true,
      },
    ];
    await t.pumpWidget(
      MaterialApp(
        home: LoginErrorConfigPage(
          environment: '线下',
          api: (r) async {
            if (r['operation'] == 'login_errors_get')
              return {
                'revision': 0,
                'messages': <String, String>{},
                'catalog': catalog,
              };
            writes.add(r);
            return {
              ...r['login_errors'] as Map,
              'revision': writes.length,
              'catalog': catalog,
            };
          },
        ),
      ),
    );
    await t.pumpAndSettle();
    await t.enterText(find.byKey(const ValueKey('server_full')), '稍后再来');
    await t.tap(find.text('保存登录提示'));
    await t.pumpAndSettle();
    expect(writes.single['login_errors']['messages'], {'server_full': '稍后再来'});
    await t.ensureVisible(find.text('恢复默认'));
    await t.tap(find.text('恢复默认'));
    await t.pumpAndSettle();
    await t.tap(find.text('保存登录提示'));
    await t.pumpAndSettle();
    expect(writes.last['login_errors']['messages'], isEmpty);
    expect(writes.last['login_errors']['revision'], 1);
    expect(find.textContaining('达到人数限制'), findsOneWidget);
  });
}
