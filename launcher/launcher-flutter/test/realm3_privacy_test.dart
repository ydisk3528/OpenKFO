import 'dart:io';
import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:openkfo_launcher/launcher_service.dart';
import 'package:openkfo_launcher/update_service.dart';
class Probe extends LauncherService {
 Probe(String root):super(root);
 List<Map<String,dynamic>> rows=[];
 int calls=0;
 @override Future<dynamic> native(Map<String,dynamic> r) async => rows;
 @override Future<String> checkHealth() async { if(calls++==0) throw const SocketException('temporary'); return 'ok'; }
}
void main() {
 test('cert repair, bridge realm isolation, and safe health retry', () async {
  final dir=await Directory.systemTemp.createTemp('realm-safety-');
  try {
   final l=Probe(dir.path)..game=dir.path..config={'url':'wss://example.test/kk/tunnel','server_certificate':'cert.crt','login_certificate':'cert.crt','login_key':'cert.crt'};
   await Directory('${dir.path}/launcher-files').create();
   final bytes=utf8.encode('bundled cert');
   await File('${dir.path}/launcher-files/cert.crt').writeAsBytes(bytes);
   l.components={'cert.crt':hashBytes(bytes),'OnlineBridge.exe':'test'};
   await File('${dir.path}/cert.crt').writeAsString('old cert');
   await l.restoreCertificates();
   expect(await File('${dir.path}/cert.crt').readAsBytes(),bytes);
   l.rows=[{'PID':100,'Created':200,'Image':l.bridgeExecutable}];
   await Directory(l.shared).create(recursive:true);
   final marker=File('${l.shared}/bridge-owner.json');
   await marker.writeAsString(jsonEncode({'PID':100,'Created':200,'Scope':await l.connectionScope()}));
   expect(await l.portConflicts(),isEmpty);
   await marker.writeAsString(jsonEncode({'PID':100,'Created':201,'Scope':await l.connectionScope()}));
   expect(await l.portConflicts(),hasLength(1));
   await marker.writeAsString('[]'); expect(await l.portConflicts(),hasLength(1));
   expect(await l.health(),'ok'); expect(l.calls,2);
   await File('${dir.path}/launcher-files/cert.crt').writeAsString('tampered');
   await expectLater(l.restoreCertificates(),throwsException);
  } finally { await dir.delete(recursive:true); }
 });
 test('load WSS realms with default or explicit port; reject invalid ports', () async {
  final dir=await Directory.systemTemp.createTemp('realm-validation-');
  try {
   final payload=await Directory('${dir.path}/launcher-files').create();
   final cert=utf8.encode('test certificate');
   await File('${payload.path}/origin.crt').writeAsBytes(cert);
   for(final entry in <String,bool>{
    'wss://example.test/kk/tunnel':true,
    'wss://example.test:443/kk/tunnel':true,
    'tls://example.test:19091':true,
    'tls://example.test':false,
    'wss://example.test:0/kk/tunnel':false,
    'wss://example.test:65536/kk/tunnel':false,
    'http://example.test/kk/tunnel':false,
   }.entries) {
    final bytes=utf8.encode(jsonEncode([{'id':'realm3','name':'三区测试','url':entry.key,'server_certificate':'origin.crt'}]));
    await File('${payload.path}/realms.json').writeAsBytes(bytes);
    final l=LauncherService(dir.path)..config={'url':entry.key}..components={'realms.json':hashBytes(bytes),'origin.crt':hashBytes(cert)};
    if(entry.value) { await l.loadRealms(); expect(l.realmId,'realm3'); expect(l.endpoint.toString(),entry.key); }
    else { await expectLater(l.loadRealms(),throwsException); }
   }
  } finally {await dir.delete(recursive:true);}
 });

 test('errors redact network addresses and preserve useful detail', () {
  final text=publicError('lookup vxfnqfjdr.top:443 failed; wss://vxfnqfjdr.top/kk/tunnel; 115.231.35.70:19091; [2001:db8::1]:443; OnlineBridge.exe HTTP 503');
  expect(text, isNot(contains('vxfnqfjdr.top')));
  expect(text, isNot(contains('115.231.35.70')));
  expect(text, isNot(contains('2001:db8')));
  expect(text,contains('OnlineBridge.exe'));expect(text,contains('HTTP 503'));
 });
 test('test package does not fetch announcement', () async {
  final l=LauncherService('.')..config={'update_enabled':false};
  expect(await UpdateService(l).announcement(),contains('暂未启用'));
 });
}
