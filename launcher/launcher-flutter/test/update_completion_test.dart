import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:openkfo_launcher/main.dart';
import 'package:openkfo_launcher/update_service.dart';
import 'package:openkfo_launcher/update_progress_view.dart';

void main() {
  testWidgets('hide progress only after installation completion', (tester) async {
    tester.view.physicalSize = const Size(1280, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(const MaterialApp(home: LauncherPage(preview: true)));
    final dynamic state = tester.state(find.byType(LauncherPage));
    state.updateTransfer(const UpdateProgress('校验更新文件', 'test.png', 1, 1, 10, 10, 10, 10));
    await tester.pump();
    expect(find.byType(UpdateProgressView), findsOneWidget);
    state.updateTransfer(const UpdateProgress('更新完成', '客户端资源', 1, 1, 10, 10, 10, 10));
    await tester.pump();
    expect(find.byType(UpdateProgressView), findsNothing);
    expect(state.transfer, isNull);
    state.updateTransfer(const UpdateProgress('更新失败，正在恢复原文件', '', 0, 1, 0, 0, 0, 0));
    await tester.pump();
    expect(find.byType(UpdateProgressView), findsOneWidget);
  });
}
