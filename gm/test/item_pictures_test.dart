import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/item_pictures.dart';

void main() {
  testWidgets('item images reuse requests and support enlarged preview', (tester) async {
    var calls = 0;
    final pictures = ItemPictures((request) async {
      calls++;
      return {'74:743001': 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII='};
    });
    final item = <String, dynamic>{'key': '74:743001', 'name': '武器切换卡'};
    await tester.pumpWidget(MaterialApp(home: Scaffold(body: Row(children: [pictures.preview(item), pictures.preview(item)]))));
    await tester.pumpAndSettle();
    expect(calls, 1);
    await tester.tap(find.byType(InkWell).first);
    await tester.pumpAndSettle();
    expect(find.text('武器切换卡'), findsOneWidget);
    expect(find.text('关闭'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
  testWidgets('missing image keeps a visible placeholder', (tester) async {
    final pictures = ItemPictures((_) async => <String, dynamic>{});
    await tester.pumpWidget(MaterialApp(home: pictures.preview({'key': 'missing'})));
    await tester.pumpAndSettle();
    expect(find.byTooltip('暂无可用图片'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}
