import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:openkfo_launcher/frame_mode.dart';
import 'package:openkfo_launcher/main.dart';

void main() {
  testWidgets('recommended default and alternative mode confirmation', (tester) async {
    await tester.pumpWidget(const MaterialApp(home: LauncherPage(preview: true)));
    final dynamic state = tester.state(find.byType(LauncherPage));
    expect(state.frameMode, FrameMode.high125);
    final dropdown = tester.widget<DropdownButtonFormField<FrameMode>>(find.byType(DropdownButtonFormField<FrameMode>));
    expect(dropdown.initialValue, FrameMode.high125);
    expect(find.textContaining('500帧'), findsNothing);
    var choice = state.selectFrameMode(FrameMode.normal);
    await tester.pumpAndSettle();
    expect(find.textContaining('玩家帧率不统一'), findsOneWidget);
    await tester.tap(find.text('使用推荐模式'));
    await tester.pumpAndSettle();
    await choice;
    expect(state.frameMode, FrameMode.high125);
    choice = state.selectFrameMode(FrameMode.normal);
    await tester.pumpAndSettle();
    await tester.tap(find.text('仍然使用'));
    await tester.pumpAndSettle();
    await choice;
    expect(state.frameMode, FrameMode.normal);
    await state.selectFrameMode(FrameMode.configZero);
    await tester.pumpAndSettle();
    expect(state.frameMode, FrameMode.high125);
    expect(tester.takeException(), isNull);
  });
}
