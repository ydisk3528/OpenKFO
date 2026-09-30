import 'dart:convert';
import 'dart:io';
import 'package:flutter_test/flutter_test.dart';
import 'package:openkfo_launcher/launcher_service.dart';
import 'package:openkfo_launcher/update_service.dart';

void main() {
  test('realm profile overrides retained legacy launcher update endpoint', () async {
    final root = await Directory.systemTemp.createTemp('oss-migration');
    addTearDown(() => root.delete(recursive: true));
    final payload = await Directory('${root.path}/launcher-files').create();
    const oss = 'https://openkfo.oss-cn-hangzhou.aliyuncs.com/version/version.json';
    final certificate = utf8.encode('test certificate');
    final profiles = utf8.encode(jsonEncode([
      {'id': 'realm2', 'name': '二区', 'url': 'tls://aaa.vxziouwkf.top:19091',
       'server_certificate': 'realm2.crt', 'launcher_update_version_url': oss}
    ]));
    await File('${payload.path}/realm2.crt').writeAsBytes(certificate);
    await File('${payload.path}/realms.json').writeAsBytes(profiles);
    final service = LauncherService(root.path)
      ..config = {'url': 'tls://aaa.vxziouwkf.top:19091',
        'update_version_url': oss,
        'launcher_update_version_url': 'https://vxziouwkf.top/launcher/version.json'}
      ..components = {'realms.json': hashBytes(profiles), 'realm2.crt': hashBytes(certificate)};
    await service.loadRealms();
    expect(service.config['launcher_update_version_url'], oss);
    expect(UpdateService(service).versionUrls, [oss]);
    expect(service.endpoint.host, 'aaa.vxziouwkf.top');
    expect(service.realms.length, 1);
  });
}
