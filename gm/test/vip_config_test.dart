import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import '../lib/vip_config.dart';

void main() {
  testWidgets(
    'VIP timeout retries exact operation and prevents duplicate input',
    (t) async {
      final grants = <Map<String, dynamic>>[];
      await t.pumpWidget(
        MaterialApp(
          home: VipConfigPage(
            environment: '本地测试服',
            uid: 123,
            account: 'testvip',
            api: (r) async {
              if (r['operation'] == 'catalog') return {'items': []};
              if (r['operation'] == 'shop_images') return <String, dynamic>{};
              if (r['operation'] == 'vip_get')
                return {'kind': 1, 'expires_at': null};
              grants.add(Map<String, dynamic>.from(r));
              if (grants.length == 1) throw Exception('timeout');
              return {'instance': 1048576};
            },
          ),
        ),
      );
      await t.pumpAndSettle();
      await t.drag(find.byType(ListView), const Offset(0, -400));
      await t.pumpAndSettle();
      await t.tap(find.text('发放会员卡'));
      await t.pumpAndSettle();
      expect(find.text('发放到 本地测试服'), findsOneWidget);
      await t.tap(find.text('确认发放'));
      await t.pumpAndSettle();
      expect(grants.length, 1);
      expect(grants.first['uid'], 123);
      expect(grants.first['vip_kind'], 2);
      expect(grants.first['id'], matches(RegExp(r'^vip-[a-f0-9]{32}$')));
      expect(t.widget<TextField>(find.byType(TextField)).enabled, isFalse);
      await t.drag(find.byType(ListView), const Offset(0, -400));
      await t.pumpAndSettle();
      await t.tap(find.text('重试同一请求'));
      await t.pumpAndSettle();
      expect(grants.length, 2);
      expect(grants[1], grants[0]);
      expect(find.textContaining('发放成功'), findsOneWidget);
    },
  );
  testWidgets('VIP failed read cannot grant', (t) async {
    await t.pumpWidget(
      MaterialApp(
        home: VipConfigPage(
          environment: '线上服务器',
          uid: 123,
          account: 'testvip',
          api: (r) async => throw Exception('offline'),
        ),
      ),
    );
    await t.pumpAndSettle();
    await t.drag(find.byType(ListView), const Offset(0, -400));
    await t.pumpAndSettle();
    expect(t.widget<FilledButton>(find.byType(FilledButton)).onPressed, isNull);
  });
}
