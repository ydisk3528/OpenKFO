import 'dart:convert';
import 'dart:io';
import 'package:flutter_test/flutter_test.dart';
import 'package:openkfo_launcher/launcher_service.dart';

void main() {
  test('realm selection changes endpoint and isolates saved accounts', () async {
    final root = await Directory.systemTemp.createTemp('realm-test');
    addTearDown(() => root.delete(recursive:true));
    final payload = Directory('${root.path}/launcher-files');
    await payload.create();
    final cert = utf8.encode('test public certificate');
    await File('${payload.path}/r2.crt').writeAsBytes(cert);
    final profiles = utf8.encode(jsonEncode([
      {'id':'one','name':'一区','url':'tls://old.example:19091','server_certificate':'r2.crt'},
      {'id':'two','name':'二区','url':'tls://new.example:19091','server_certificate':'r2.crt','launcher_update_version_url':'https://new.example/launcher/version.json'},
    ]));
    await File('${payload.path}/realms.json').writeAsBytes(profiles);
    LauncherService make() => LauncherService(root.path)
      ..config={'url':'tls://old.example:19091','credentials_scope':'legacy-one','update_version_url':'https://example.com/version.json'}
      ..components={'realms.json':hashBytes(profiles),'r2.crt':hashBytes(cert)};
    final first = make();
    await first.loadRealms();
    expect(first.realmId, 'one');
    final firstCredentials = first.credentialsPath(1);
    await first.realmSelection.writeAsString('{"id":"two"}');
    final second = make(); await second.loadRealms();
    expect(second.endpoint.host, 'new.example');
    expect(second.credentialsPath(1), isNot(firstCredentials));
    expect(second.config['update_version_url'], 'https://example.com/version.json');
    expect(second.config['server_certificate'], contains('launcher-files'));
    final fixedProfile = utf8.encode(jsonEncode([
      {'id':'two','name':'二区','url':'tls://new.example:19091','server_certificate':'r2.crt','launcher_update_version_url':'https://new.example/launcher/version.json'},
    ]));
    await File('${payload.path}/realms.json').writeAsBytes(fixedProfile);
    await second.realmSelection.writeAsString('{"id":"one"}');
    final fixed = make()..components['realms.json'] = hashBytes(fixedProfile);
    await fixed.loadRealms();
    expect(fixed.realms.length, 1);
    expect(fixed.endpoint.host, 'new.example');
    expect(fixed.config['launcher_update_version_url'], 'https://new.example/launcher/version.json');
    expect(fixed.credentialsPath(1), second.credentialsPath(1));
    await File('${payload.path}/realms.json').writeAsBytes(profiles);
    await File('${payload.path}/r2.crt').writeAsString('tampered');
    await expectLater(make().loadRealms(), throwsException);
  });
}
