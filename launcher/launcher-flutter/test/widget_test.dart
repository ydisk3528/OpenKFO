import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:openkfo_launcher/main.dart';
import 'package:openkfo_launcher/launcher_service.dart';
import 'package:openkfo_launcher/update_service.dart';
import 'package:openkfo_launcher/log_export.dart';
import 'package:openkfo_launcher/update_progress_view.dart';

class FakeUpdates extends UpdateService {
  final Map<String, List<int>> payloads;
  final fetched = <String>[];
  FakeUpdates(super.launcher, this.payloads, {super.onProgress});
  @override
  Future<List<int>?> download(Uri url, int max, {bool missing = false, void Function(int)? onBytes}) async {
    fetched.add(url.path);
    final bytes=payloads[url.pathSegments.last];
    onBytes?.call(0);
    if(bytes!=null)onBytes?.call(bytes.length);
    return bytes;
  }
}

class ComponentTestLauncher extends LauncherService {
  ComponentTestLauncher(super.root);
  @override
  Future<void> validateClientExecutable() async {
    if (!await File('$game/gfld.dat').exists()) throw StateError('missing test client');
  }
}

void main() {
  testWidgets('custom title controls dispatch native actions and menu stays left', (tester) async {
    const channel=MethodChannel('launcher/window');
    final calls=<String>[];
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger.setMockMethodCallHandler(channel,(call) async {
      calls.add(call.method); return call.method=='maximize';
    });
    addTearDown(() => TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger.setMockMethodCallHandler(channel,null));
    await tester.pumpWidget(const MaterialApp(home: LauncherPage(preview:true)));
    expect(find.text('V1.2'), findsOneWidget);
    expect(tester.getTopLeft(find.text('检查更新')).dx,lessThan(50));
    for(final tip in ['最小化','最大化','关闭启动器']) {
      await tester.tap(find.byTooltip(tip)); await tester.pump();
    }
    expect(calls,['minimize','maximize','close']);
    expect(find.byTooltip('还原窗口'),findsOneWidget);
    expect(tester.takeException(),isNull);
  });
  testWidgets('mandatory update has no skip or dismiss path', (tester) async {
    await tester.pumpWidget(const MaterialApp(home: LauncherPage(preview: true)));
    final dynamic state = tester.state(find.byType(LauncherPage));
    final Future<void> dialog = state.requireUpdate('必须更新', '更新说明');
    await tester.pumpAndSettle();
    expect(find.text('立即更新'), findsOneWidget);
    expect(find.textContaining('跳过'), findsNothing);
    await tester.tapAt(const Offset(1, 1));
    await tester.pumpAndSettle();
    expect(find.text('立即更新'), findsOneWidget);
    await tester.tap(find.text('立即更新'));
    await tester.pumpAndSettle();
    await dialog;
  });

  test('directory selection requires real game files and persists only valid choices', () async {
    final root = await Directory.systemTemp.createTemp('directory-choice-');
    addTearDown(() => root.delete(recursive: true));
    final game = await Directory('${root.path}/arbitrary-name').create();
    final service = LauncherService(root.path)..game = root.path;
    expect(await LauncherService.isGameDirectory(game.path), false);
    await writeAtomic('${game.path}/Data/config.spf2', [1]);
    expect(await LauncherService.isGameDirectory(game.path), false);
    await expectLater(service.selectGameDirectory(game.path), throwsA(isA<GameDirectoryError>()));
    expect(await service.gameDirectoryFile.exists(), false);
    await Directory('${game.path}/gfxz.dat').create();
    expect(await LauncherService.isGameDirectory(game.path), false);
    await File('${game.path}/gfld.dat').writeAsBytes([]);
    expect(await LauncherService.isGameDirectory(game.path), false);
    await File('${game.path}/gfld.dat').writeAsBytes([2]);
    await service.selectGameDirectory(game.path);
    await service.validate();
    final selected = service.game;
    final restored = LauncherService(root.path)..game = root.path;
    await restored.restoreGameDirectory();
    expect(restored.game, selected);
    await expectLater(service.selectGameDirectory(root.path), throwsA(isA<GameDirectoryError>()));
    expect(service.game, selected);
    await File('${game.path}/gfld.dat').delete();
    await expectLater(service.validate(), throwsA(isA<GameDirectoryError>()));
  });

  test('FPS helper versions preserve running binaries across updates', () async {
    final dir = await Directory.systemTemp.createTemp('fps-helper-');
    addTearDown(() => dir.delete(recursive: true));
    final service = LauncherService(dir.path)..game = dir.path;
    await File(service.support).writeAsBytes([1, 2]);
    final old = await service.fpsExecutable();
    expect(await service.fpsExecutable(), old);
    await File(service.support).writeAsBytes([3, 4]);
    final updated = await service.fpsExecutable();
    expect(updated, isNot(old));
    expect(await File(old).readAsBytes(), [1, 2]);
    expect(await File(updated).readAsBytes(), [3, 4]);
  });
  test('announcement is independent of update manifest and tolerates missing file', () async {
    final service = LauncherService('.')..config = {
      'update_version_url': 'https://example.invalid/version/version.json',
    };
    final updates = FakeUpdates(service, {'announcement.json': utf8.encode(jsonEncode({
      'title': '维护公告', 'content': '今晚测试更新',
    }))});
    expect(await updates.announcement(), '维护公告\n今晚测试更新');
    expect(updates.fetched, ['/announcement.json']);
    expect(await FakeUpdates(service, {}).announcement(), '暂无公告');
    await expectLater(FakeUpdates(service, {'announcement.json': utf8.encode('{"content":42}')}).announcement(), throwsFormatException);
  });

  test('missing or unknown native client is rejected without installing one', () async {
    final dir = await Directory.systemTemp.createTemp('native-client-');
    addTearDown(() => dir.delete(recursive: true));
    final service = LauncherService(dir.path)..game = dir.path;
    await expectLater(service.validateClientExecutable(), throwsException);
    expect(await File('${dir.path}/gfld.dat').exists(), false);
    await File('${dir.path}/gfld.dat').writeAsBytes([1]);
    await expectLater(service.validateClientExecutable(), throwsException);
    expect(await File('${dir.path}/gfld.dat').readAsBytes(), [1]);
  });

  test('client selection prefers existing gfxz and falls back to gfld', () async {
    final dir = await Directory.systemTemp.createTemp('client-selection-');
    addTearDown(() => dir.delete(recursive: true));
    final service = LauncherService(dir.path);
    service.game = dir.path;
    service.config = {'client_executable': 'gfld.dat'};
    expect(service.clientExecutable, 'gfld.dat');
    await File('${dir.path}/gfld.dat').writeAsBytes([1]);
    final gfxz = File('${dir.path}/gfxz.dat');
    await gfxz.writeAsBytes([2]);
    expect(service.clientExecutable, 'gfxz.dat');
    await gfxz.delete();
    service.config['client_executable'] = 'gfxz.dat';
    expect(service.clientExecutable, 'gfld.dat');
    await Directory('${dir.path}/gfxz.dat').create();
    expect(service.clientExecutable, 'gfld.dat');
  });
  test('TXT export includes full Chinese log and hides network endpoints',()async {
    final dir=await Directory.systemTemp.createTemp('log-export-');addTearDown(()=>dir.delete(recursive:true));
    final source=File('${dir.path}/online-client.log');await source.writeAsString('开始日志\n${List.filled(4000,"玩家动作 中文\n").join()}https://example.invalid:443/test\n结束日志');
    final path='${dir.path}/日志.txt';await exportLog(source,path);final bytes=await File(path).readAsBytes();expect(bytes.take(3),[0xef,0xbb,0xbf]);final text=utf8.decode(bytes.skip(3).toList());expect(text,contains('开始日志'));expect(text,contains('结束日志'));expect(text,contains('[连接地址]'));expect(text, isNot(contains('example.invalid')));expect(bytes.length,greaterThan(32768));
  });
  testWidgets('update details show both progress bars and byte counts',(tester)async {
    await tester.pumpWidget(const MaterialApp(home:Scaffold(body:UpdateProgressView(UpdateProgress('下载更新文件','Weapon/test.dat',2,4,512,1024,1536,4096)))));
    expect(find.text('文件 2 / 4'),findsOneWidget);expect(find.text('Weapon/test.dat'),findsOneWidget);expect(find.byType(LinearProgressIndicator),findsNWidgets(2));
    final bars=tester.widgetList<LinearProgressIndicator>(find.byType(LinearProgressIndicator)).toList();expect(bars[0].value,.5);expect(bars[1].value,.375);expect(tester.takeException(),isNull);
  });

  testWidgets('small window keeps registration hint and controls visible', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(760, 660);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(
      const MaterialApp(home: LauncherPage(preview: true)),
    );
    await tester.pump();
    expect(find.byTooltip('进入游戏'), findsOneWidget);
    expect(find.text('更新公告'), findsOneWidget);
    await tester.tap(find.text('设置'));
    await tester.pumpAndSettle();
    expect(find.textContaining('账号不存在时'), findsOneWidget);
    expect(find.text('设置游戏目录'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
  test(
    'only changed logic downloads; corrupt component never changes live files',
    () async {
      final root = await Directory.systemTemp.createTemp('update-test-');
      addTearDown(() => root.delete(recursive: true));
      final stage = await Directory('${root.path}/staged').create();
      final service = LauncherService(root.path)
        ..config = {
          'url': 'tls://example.invalid:19091',
          'update_base_url': 'https://example.invalid/updates/',
        };
      final files = <String, dynamic>{};
      for (final name in [
        '启动器.exe',
        'LauncherSupport.exe',
        'data/app.so',
        'flutter_windows.dll',
      ]) {
        final old = File('${root.path}/$name');
        await old.parent.create(recursive: true);
        await old.writeAsBytes([1]);
        final next = name == 'data/app.so' ? [2] : [1];
        files[name] = {'sha256': hashBytes(next), 'size': 1};
      }
      final manifest = <String, dynamic>{
        'kind': 'launcher-flutter',
        'version': 'test',
        'notes': 'test',
        'files': files,
      };
      final events=<UpdateProgress>[];
      final update = FakeUpdates(service, {
        '${hashBytes([2])}.bin': [2],
      },onProgress:events.add);
      final changed = await update.stageChanges(manifest, stage, (_) {});
      expect(changed.length, 1);
      expect(changed.single['Name'], 'data/app.so');
      expect(events.first.received,0);
      expect(events.last.phase,'校验更新文件');
      expect(events.last.overallFraction,1);
      expect(events.every((v)=>v.count==1 && v.file=='data/app.so'),true);
      expect(update.fetched.length, 1);
      expect(await File('${root.path}/data/app.so').readAsBytes(), [1]);
      final corrupt = FakeUpdates(service, {
        '${hashBytes([2])}.bin': [3],
      });
      await expectLater(
        corrupt.stageChanges(manifest, stage, (_) {}),
        throwsException,
      );
      expect(await File('${root.path}/data/app.so').readAsBytes(), [1]);
    },
  );
  test(
    'component preparation repairs login files and preserves other settings',
    () async {
      final dir = await Directory.systemTemp.createTemp('launcher-test-');
      addTearDown(() => dir.delete(recursive: true));
      final payload = Directory('${dir.path}/launcher-files');
      await payload.create();
      final contents = <String, List<int>>{
        'SDError.dll': [4],
        'lqbz.dll': [7],
        'libssl-1_1.dll': [5],
        'libcrypto-1_1.dll': [6],
        'runtime-x86/vcruntime140.dll': [11],
        'runtime-x86/ucrtbase.dll': [12],
        'OnlineBridge.exe': [7],
        'GameMod.exe': [71],
        'client-config.xml': utf8.encode('<LoginServer Port="18000"/>'),
        'bridge.json': utf8.encode(
          jsonEncode({
            'client_directory': '.',
            'url': 'tls://example.invalid:19091',
            'shared_client': true,
            'login_certificate': 'launcher-certificates/online/login.crt',
          }),
        ),
        'launcher-certificates/online/origin.crt': [8],
        'launcher-certificates/online/login.crt': [9],
        'launcher-certificates/online/login.key': [10],
      };
      for (final e in contents.entries) {
        final f = File('${payload.path}/${e.key}');
        await f.parent.create(recursive: true);
        await f.writeAsBytes(e.value);
      }
      await File('${payload.path}/files.json').writeAsString(
        jsonEncode(contents.map((k, v) => MapEntry(k, hashBytes(v)))),
      );
      await Directory('${dir.path}/Data').create();
      await File('${dir.path}/Data/config.spf2').writeAsBytes([42]);
      await File('${dir.path}/Settings.xml').writeAsString(
        '<Settings><LoginServer Index="2"/><Audio Volume="17"/></Settings>',
      );
      await File('${dir.path}/gfld.dat').writeAsBytes([1, 2, 3]);
      final service = ComponentTestLauncher(dir.path);
      await service.init();
      final legacyBridge = File('${service.shared}/OnlineBridge.exe');
      await legacyBridge.parent.create(recursive: true);
      await legacyBridge.writeAsBytes([90]);
      await service.prepare();
      expect(await File('${File(service.bridgeExecutable).parent.path}/GameMod.exe').readAsBytes(), [71]);
      expect(await legacyBridge.readAsBytes(), [90]);
      final firstBridge = service.bridgeExecutable;
      expect(await File(firstBridge).readAsBytes(), [7]);
      await File('${payload.path}/OnlineBridge.exe').writeAsBytes([70]);
      service.components['OnlineBridge.exe'] = hashBytes([70]);
      await service.prepare();
      expect(service.bridgeExecutable, isNot(firstBridge));
      expect(await File(firstBridge).readAsBytes(), [7]);
      expect(await File(service.bridgeExecutable).readAsBytes(), [70]);
      expect(await legacyBridge.readAsBytes(), [90]);
      expect(await File('${dir.path}/SDError.dll').readAsBytes(), [4]);
      expect(await File('${dir.path}/lqbz.dll').readAsBytes(), [7]);
      expect(await File('${dir.path}/zz.crt').readAsBytes(), [9]);
      expect(await File('${dir.path}/vcruntime140.dll').readAsBytes(), [11]);
      expect(await File('${dir.path}/ucrtbase.dll').readAsBytes(), [12]);
      expect(
        await File('${dir.path}/Settings.xml').readAsString(),
        contains('Index="0"'),
      );
      expect(
        await File('${dir.path}/Settings.xml').readAsString(),
        contains('Volume="17"'),
      );
      await File('${dir.path}/SDError.dll').writeAsBytes([99]);
      await service.prepare();
      expect(await File('${dir.path}/SDError.dll').readAsBytes(), [4]);
      expect(await File('${dir.path}/lqbz.dll').readAsBytes(), [7]);
      await File('${payload.path}/SDError.dll').writeAsBytes([99]);
      await expectLater(service.prepare(), throwsException);
    },
  );
  test('update paths reject configuration replacement and traversal', () {
    for (final path in [
      '../x.dll',
      'data/../x',
      'C:/x.dll',
      'bridge.json',
      'a\\b.dll',
      'data/x. /y',
    ]) {
      expect(allowedLauncher(path), false, reason: path);
    }
    expect(allowedLauncher('data/app.so'), true);
    expect(allowedLauncher('launcher-files/LoginSkin.dll'), true);
    expect(allowedClient('Data/config.spf2'), true);
    expect(allowedClient('gfld.dat'), false);
  });
  test('wrong directory has a friendly message', () async {
    final d = await Directory.systemTemp.createTemp();
    addTearDown(() => d.delete());
    final s = LauncherService(d.path)..game = d.path;
    await expectLater(s.validate(), throwsA(isA<GameDirectoryError>()));
    expect(GameDirectoryError().toString(), isNot(contains('Exception')));
  });
}
