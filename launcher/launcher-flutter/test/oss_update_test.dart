import 'dart:convert';
import 'dart:io';
import 'package:flutter_test/flutter_test.dart';
import 'package:openkfo_launcher/launcher_service.dart';
import 'package:openkfo_launcher/update_service.dart';

class OfflineLauncher extends LauncherService {
  OfflineLauncher(super.root);
  @override
  Future<Map<String, dynamic>?> state(int n) async => null;
}
class OssFixture extends UpdateService {
  OssFixture(super.launcher, this.payloads);
  final Map<String, List<int>> payloads;
  final fetched = <String>[];
  @override
  Future<List<int>?> download(Uri url, int max, {bool missing = false, void Function(int)? onBytes}) async {
    fetched.add(url.toString());
    final bytes = payloads[url.toString()];
    if (bytes == null) throw StateError('Missing object: $url');
    if (bytes.length > max) throw StateError('Oversized');
    onBytes?.call(bytes.length);
    return bytes;
  }
}
void main() {
  const base = 'https://openkfo.oss-cn-hangzhou.aliyuncs.com/';
  test('skipping release A installs its retained resources together with release B', () async {
    final root = await Directory.systemTemp.createTemp('oss-skip-release-');
    addTearDown(() => root.delete(recursive: true));
    final service = OfflineLauncher(root.path)..game = root.path..config = {
      'url':'tls://example.invalid:19091', 'update_version_url':'${base}version/version.json',
    };
    await writeAtomic('${root.path}/Data/config.spf2', [1]);
    await writeAtomic('${root.path}/Data/old.dat', [9]);
    final manifest = {'version':'B','target':'client','config_hash':hashBytes([2]),'files':[
      {'path':'Data/config.spf2','url':'${base}releases/B/client/Data/config.spf2','size':1,'sha256':hashBytes([2])},
      {'path':'Data/map-A.dat','url':'${base}releases/A/client/Data/map-A.dat','size':1,'sha256':hashBytes([3])},
      {'path':'Data/old.dat','url':'${base}releases/A/client/Data/old.dat','size':1,'sha256':hashBytes([9])},
    ]};
    final update = OssFixture(service, {
      '${base}version/version.json':utf8.encode(jsonEncode({'version':'B','client_manifest':'${base}manifest/B/client.json'})),
      '${base}manifest/B/client.json':utf8.encode(jsonEncode(manifest)),
      '${base}releases/B/client/Data/config.spf2':[2],
      '${base}releases/A/client/Data/map-A.dat':[3],
    });
    final pending = await update.clientCheck();
    expect(pending, isNotNull);
    await update.installClient(pending!, (_) {});
    expect(await File('${root.path}/Data/map-A.dat').readAsBytes(), [3]);
    expect(await File('${root.path}/Data/config.spf2').readAsBytes(), [2]);
    expect(await update.clientCheck(), isNull);
    expect(update.fetched.where((v) => v.contains('/releases/')).length, 2);
  });
  test('OSS pointer resolves immutable manifest and downloads only changed file', () async {
    final root = await Directory.systemTemp.createTemp('oss-update-test-');
    addTearDown(() => root.delete(recursive: true));
    final service = OfflineLauncher(root.path)..config = {'url': 'tls://example.invalid:19091', 'update_version_url': '${base}version/version.json'};
    final rows = <Map<String,dynamic>>[];
    for (final path in ['启动器.exe','LauncherSupport.exe','data/app.so','flutter_windows.dll']) {
      await writeAtomic('${root.path}/$path', [1]);
      final bytes = path == 'data/app.so' ? [2] : [1];
      rows.add({'path': path, 'url': '${base}releases/v2/$path', 'size': 1, 'sha256': hashBytes(bytes)});
    }
    final fixture = OssFixture(service, {
      '${base}version/version.json': utf8.encode(jsonEncode({'version':'v2','manifest':'${base}manifest/v2/launcher.json'})),
      '${base}manifest/v2/launcher.json': utf8.encode(jsonEncode({'version':'v2','target':'launcher','files': rows})),
      '${base}releases/v2/data/app.so': [2],
    });
    final manifest = await fixture.check();
    expect(manifest, isNotNull);
    final stage = await Directory('${root.path}/staging').create();
    final changed = await fixture.stageChanges(manifest!, stage, (_) {});
    expect(changed.single['Name'], 'data/app.so');
    expect(fixture.fetched.length, 3);
    expect(await File('${root.path}/data/app.so').readAsBytes(), [1]);
    expect(await File('${stage.path}/data/app.so').readAsBytes(), [2]);
    fixture.payloads['${base}releases/v2/data/app.so'] = [3];
    await expectLater(fixture.stageChanges(manifest, stage, (_) {}), throwsException);
    expect(await File('${root.path}/data/app.so').readAsBytes(), [1]);
  });
  test('OSS validates version, transport, duplicate names and client paths', () {
    final row = {'path':'Data/config.spf2','url':'${base}releases/v2/config','sha256':hashBytes([1]),'size':1};
    Map<String,dynamic> make(List<dynamic> rows) => {'target':'client','version':'v2','config_hash':hashBytes([1]),'files':rows};
    final valid = UpdateService.normalizeOss(make([row]),'v2',client:true);
    UpdateService.validateClientFiles(valid);
    expect(() => UpdateService.normalizeOss(make([row]),'v3',client:true), throwsFormatException);
    expect(() => UpdateService.normalizeOss(make([row,{...row,'path':'data/CONFIG.SPF2'}]),'v2',client:true), throwsFormatException);
    expect(() => UpdateService.normalizeOss(make([{...row,'url':'http://example.invalid/file'}]),'v2',client:true), throwsFormatException);
    final unsafe = UpdateService.normalizeOss(make([{...row,'path':'../escape.dll'}]),'v2',client:true);
    expect(() => UpdateService.validateClientFiles(unsafe), throwsFormatException);
  });
  test('client files install only after every download verifies', () async {
    final root = await Directory.systemTemp.createTemp('oss-client-test-');
    addTearDown(() => root.delete(recursive:true));
    final service = OfflineLauncher(root.path)..game = root.path..config = {'config_hash':hashBytes([1])};
    await writeAtomic('${root.path}/Data/config.spf2', [1]);
    await writeAtomic('${root.path}/Weapon/test.dat', [1]);
    final manifest = UpdateService.normalizeOss({'version':'v2','target':'client','config_hash':hashBytes([1]),'files':[
      {'path':'Data/config.spf2','url':'${base}releases/v2/config','size':1,'sha256':hashBytes([1])},
      {'path':'Weapon/test.dat','url':'${base}releases/v2/weapon','size':1,'sha256':hashBytes([2])},
    ]},'v2',client:true);
    final update = OssFixture(service, {'${base}releases/v2/weapon':[3]});
    await expectLater(update.installClient(manifest, (_) {}), throwsException);
    expect(await File('${root.path}/Weapon/test.dat').readAsBytes(), [1]);
    update.payloads['${base}releases/v2/weapon'] = [2];
    await update.installClient(manifest, (_) {});
    expect(await File('${root.path}/Weapon/test.dat').readAsBytes(), [2]);
    expect(update.fetched.every((url) => url.endsWith('/weapon')), true);
  });
}
