import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:openkfo_launcher/frame_mode.dart';
import 'package:openkfo_launcher/main.dart';
void main() {
 testWidgets('launcher only exposes fixed 125 FPS', (tester) async {
  await tester.pumpWidget(const MaterialApp(home:LauncherPage(preview:true)));
  final dynamic state=tester.state(find.byType(LauncherPage));
  expect(state.frameMode,FrameMode.high125);
  final selector=find.byType(DropdownButtonFormField<FrameMode>);
  expect(selector,findsOneWidget);
  final field=tester.widget<DropdownButtonFormField<FrameMode>>(selector);
  expect(field.initialValue,FrameMode.high125);
  expect(find.text('帧率模式'),findsOneWidget);
  expect(find.text('普通模式'),findsNothing);
  expect(find.textContaining('500帧'),findsNothing);
  expect(find.text('约 125 FPS（实验性功能）'),findsOneWidget);
  expect(tester.takeException(),isNull);
 });
}
