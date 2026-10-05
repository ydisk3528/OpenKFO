import 'dart:async';
import 'dart:io';
import 'package:flutter_test/flutter_test.dart';
import 'package:openkfo_launcher/launcher_service.dart';

void main() {
  test('native reply drains stderr and bounds a stalled helper', () async {
    Future<Process> helper(String code) => Process.start('powershell.exe', [
      '-NoProfile', '-NonInteractive', '-WindowStyle', 'Hidden', '-Command', code,
    ]);
    final success = await helper(r'''$null=[Console]::In.ReadToEnd(); [Console]::Error.Write(('x'*131072)); [Console]::Out.Write('{"ok":true,"result":42}')''');
    expect(await LauncherService.nativeReply(success, {'Op':'info'}), 42);
    final stalled = await helper('Start-Sleep -Seconds 60');
    await expectLater(
      LauncherService.nativeReply(stalled, {'Op':'info'}, timeout: const Duration(milliseconds: 300)),
      throwsA(isA<TimeoutException>()),
    );
    await stalled.exitCode.timeout(const Duration(seconds: 5));
  }, skip: !Platform.isWindows);
}
