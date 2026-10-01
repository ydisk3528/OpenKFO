import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import '../lib/horn_config.dart';

void main() {
  testWidgets('independent switches saved to server', (tester) async {
    Map<String, dynamic>? sent;
    final data = <String, dynamic>{
      'revision': 1,
      'channel_enabled': true,
      'realm_enabled': true,
      'mood_enabled': true,
    };
    await tester.pumpWidget(
      MaterialApp(
        home: HornConfigPage(
          environment: '线下',
          api: (r) async {
            if (r['operation'] == 'horn_save')
              sent = Map<String, dynamic>.from(r['horn'] as Map);
            return sent ?? data;
          },
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('全区喇叭'));
    await tester.pump();
    await tester.tap(find.text('保存配置'));
    await tester.pumpAndSettle();
    expect(sent?['channel_enabled'], true);
    expect(sent?['realm_enabled'], false);
    expect(sent?['mood_enabled'], true);
  });
}
