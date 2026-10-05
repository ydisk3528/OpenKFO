import 'dart:convert';
import 'dart:io';
import 'package:flutter_test/flutter_test.dart';
import 'package:openkfo_launcher/launcher_service.dart';

void main() {
 test('embedded realm loads and verifies without certificate files', () async {
  final dir=await Directory.systemTemp.createTemp('embedded-certs-');
  addTearDown(()=>dir.delete(recursive:true));
  final service=LauncherService(dir.path)..config={
   'url':'tls://example.test:19091','embedded_certificates':true,
   'server_certificate':'launcher-certificates/online/origin.crt',
   'login_certificate':'launcher-certificates/online/login.crt',
   'login_key':'launcher-certificates/online/login.key',
  };
  final realms=utf8.encode(jsonEncode([{'id':'realm3','name':'三区','url':'tls://example.test:19091','server_certificate':'launcher-certificates/realm3/origin.crt'}]));
  await writeAtomic('${dir.path}/launcher-files/realms.json',realms);
  service.components={'realms.json':hashBytes(realms)};
  await service.loadRealms();
  await service.restoreCertificates();
  final pin=await service.verifiedCertificate('server_certificate');
  SecurityContext(withTrustedRoots:false).setTrustedCertificatesBytes(pin);
  expect(await Directory('${dir.path}/launcher-certificates').exists(),false);
  expect(await Directory('${dir.path}/launcher-files/launcher-certificates').exists(),false);
  service.config['server_certificate']='other.crt';
  await File('${dir.path}/other.crt').writeAsBytes(pin);
  await expectLater(service.verifiedCertificate('server_certificate'),throwsException);
 });
}
