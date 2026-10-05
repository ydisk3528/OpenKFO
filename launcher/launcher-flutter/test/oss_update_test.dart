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
  test('receipt write failure rolls back resources and config hash together', () async {
    final root = await Directory.systemTemp.createTemp('update-rollback-');
    addTearDown(() => root.delete(recursive: true));
    final service = OfflineLauncher(root.path)..game = root.path..config = {
      'url': 'tls://example.invalid:19091', 'config_hash': hashBytes([1]),
    };
    final original = jsonEncode(service.config);
    await File(service.configPath).writeAsString(original);
    await writeAtomic('${root.path}/Data/config.spf2', [1]);
    // An unwritable receipt destination fails after the new hash was saved.
    await Directory('${root.path}/flutter-client-installed.json').create();
    final manifest = UpdateService.normalizeOss({'version':'v2','target':'client','config_hash':hashBytes([2]),'files':[
      {'path':'Data/config.spf2','url':'${base}releases/v2/config','size':1,'sha256':hashBytes([2])},
    ]}, 'v2', client:true);
    final update = OssFixture(service, {'${base}releases/v2/config':[2]});
    await expectLater(update.installClient(manifest, (_) {}), throwsA(isA<FileSystemException>()));
    expect(await File('${root.path}/Data/config.spf2').readAsBytes(), [1]);
    expect(await File(service.configPath).readAsString(), original);
    expect(service.config['config_hash'], hashBytes([1]));
  });
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
  test('server source is used first and OSS is the fallback', () async {
    const server = 'https://game.example/dl/';
    final root = await Directory.systemTemp.createTemp('server-source-test-');
    addTearDown(() => root.delete(recursive: true));
    final service = OfflineLauncher(root.path)..game = root.path..config = {
      'url': 'tls://example.invalid:19091',
      'update_version_url': '${base}version/version.json',
      'update_version_urls': ['${server}version/version.json'],
    };
    await writeAtomic('${root.path}/Data/config.spf2', [1]);
    List<int> pointer(String b) => utf8.encode(jsonEncode({'version': 'v1', 'client_manifest': '${b}manifest/v1/client.json'}));
    List<int> manifest(String b) => utf8.encode(jsonEncode({'version': 'v1', 'target': 'client', 'config_hash': hashBytes([1]),
      'files': [{'path': 'Data/config.spf2', 'url': '${b}releases/v1/client/Data/config.spf2', 'size': 1, 'sha256': hashBytes([1])}]}));
    final both = OssFixture(service, {
      '${server}version/version.json': pointer(server), '${server}manifest/v1/client.json': manifest(server),
      '${base}version/version.json': pointer(base), '${base}manifest/v1/client.json': manifest(base),
    });
    expect(both.versionUrls, ['${server}version/version.json', '${base}version/version.json']);
    expect(await both.clientCheck(), isNull);
    expect(both.fetched.every((url) => url.startsWith(server)), true);
    // Server unreachable: the same release is read from OSS and stays there.
    final ossOnly = OssFixture(service, {'${base}version/version.json': pointer(base), '${base}manifest/v1/client.json': manifest(base)});
    expect(await ossOnly.clientCheck(), isNull);
    expect(ossOnly.verifiedClientVersion, 'v1');
    ossOnly.fetched.clear();
    await ossOnly.clientCheck();
    expect(ossOnly.fetched.first, '${base}version/version.json');
    await expectLater(OssFixture(service, {}).clientCheck(), throwsStateError);
  });
  test('server source is off by default: init adds no extra update entry', () async {
    final root = await Directory.systemTemp.createTemp('default-source-test-');
    addTearDown(() => root.delete(recursive: true));
    final payload = Directory('${root.path}/launcher-files')..createSync();
    final files = <String, String>{};
    for (final name in ['bridge.json', 'launcher-certificates/online/origin.crt',
        'launcher-certificates/online/login.crt', 'launcher-certificates/online/login.key']) {
      final bytes = utf8.encode(name == 'bridge.json' ? jsonEncode({'url': 'tls://example.invalid:19091', 'shared_client': true}) : name);
      await writeAtomic('${payload.path}/$name', bytes);
      files[name] = hashBytes(bytes);
    }
    await File('${payload.path}/files.json').writeAsString(jsonEncode(files));
    final service = OfflineLauncher(root.path);
    await service.init();
    expect(service.config.containsKey('update_version_urls'), false);
    expect(UpdateService(service).versionUrls, ['${base}version/version.json']);
  });
  test('without update_version_urls only the original OSS entry is used', () {
    final service = LauncherService('.')..config = {'update_version_url': '${base}version/version.json'};
    expect(UpdateService(service).versionUrls, ['${base}version/version.json']);
    expect(UpdateService(LauncherService('.')..config = {}).usesOss, false);
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
