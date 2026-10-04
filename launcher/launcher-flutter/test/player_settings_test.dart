import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:openkfo_launcher/launcher_service.dart';
import 'package:openkfo_launcher/frame_mode.dart';

class SettingsLauncher extends LauncherService {
  SettingsLauncher(String directory) : super(directory) { game = directory; }
  bool running = false;
  Future<void> Function()? concurrentEdit;
  @override
  Future<Map<String, dynamic>?> state(int n) async {
    if (n == 1) await concurrentEdit?.call();
    return running ? {'PID': 1} : null;
  }
}

void main() {
  test('saved keys restore before launch and polling preserves other settings', () async {
    final dir = await Directory.systemTemp.createTemp('login-keys-');
    addTearDown(() => dir.delete(recursive: true));
    final service = SettingsLauncher(dir.path);
    final file = File('${dir.path}/Settings.xml');
    final names = ['Up','Down','Left','Right','Aim','LAttack','WAttack','Jump','Defence','Skill','ConsumeWeaopon1','ConsumeWeaopon2','SwitchWeapon','Burst'];
    final block = '<OperationsSettings>${names.map((n) => '<$n Key="65"></$n>').join()}</OperationsSettings>';
    await file.writeAsString('<Settings>$block<RenderIntervel Intervel="0"/></Settings>');
    await service.restoreKeySettings();
    final saved = await service.keySettingsFile.readAsBytes();
    final changed = block.replaceFirst('Jump Key="65"', 'Jump Key="86"');
    await file.writeAsString('<Settings>$changed<RenderIntervel Intervel="1"/></Settings>');
    await service.captureKeySettings(); // Unarmed: never overwrite the previous session's keys.
    expect(await service.keySettingsFile.readAsBytes(), saved);
    await service.restoreKeySettings();
    expect(await file.readAsString(), '<Settings>$block<RenderIntervel Intervel="1"/></Settings>');
    service.keySettingsGame = dir.path;
    await file.writeAsString('<Settings>$changed<RenderIntervel Intervel="1"/></Settings>');
    await service.captureKeySettings();
    expect(await service.keySettingsFile.readAsString(), changed);
    await file.writeAsString('<Settings><OperationsSettings>');
    await expectLater(service.captureKeySettings(), throwsStateError);
    expect(await service.keySettingsFile.readAsString(), changed);
  });
  test('player settings preserve bytes, backups and concurrent edits', () async {
    final dir = await Directory.systemTemp.createTemp('player-settings-');
    addTearDown(() => dir.delete(recursive: true));
    final service = SettingsLauncher(dir.path);
    final file = File('${dir.path}/Settings.xml');
    // GBK bytes and custom key bindings must survive without XML reserialization.
    final original = latin1.encode('<?xml version="1.0" encoding="GB2312"?>'
        '<Settings><!--\xD6\xD0\xCE\xC4--><OperationsSettings Jump="32" Attack="74"/>'
        '<RenderIntervel Intervel="1"/></Settings>');
    await file.writeAsBytes(original);
    final stamp = DateTime(2020);
    await file.setLastModified(stamp);
    await service.updatePlayerSettings((text) => text);
    expect(await file.lastModified(), stamp);
    final backup = File('${dir.path}/launcher-components/backups/${hashBytes(original)}/Settings.xml');
    expect(await backup.readAsBytes(), original);

    await service.updatePlayerSettings((text) => applyFrameMode(text, FrameMode.configZero));
    expect(await file.readAsBytes(), latin1.encode(latin1.decode(original).replaceFirst('Intervel="1"', 'Intervel="0"')));
    expect(await backup.readAsBytes(), original);
    final changed = await file.readAsBytes();
    service.running = true;
    await service.updatePlayerSettings((text) => text); // Same-mode multi-open remains allowed.
    await expectLater(service.updatePlayerSettings((text) => applyFrameMode(text, FrameMode.normal)), throwsStateError);
    expect(await file.readAsBytes(), changed);

    service.running = false;
    final playerEdit = latin1.encode(latin1.decode(changed).replaceFirst('Jump="32"', 'Jump="86"'));
    service.concurrentEdit = () async { await file.writeAsBytes(playerEdit); };
    await expectLater(service.updatePlayerSettings((text) => applyFrameMode(text, FrameMode.normal)), throwsStateError);
    expect(await file.readAsBytes(), playerEdit);
    expect(await backup.readAsBytes(), original);
  });
}
