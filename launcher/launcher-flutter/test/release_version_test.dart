import 'package:flutter_test/flutter_test.dart';
import 'package:openkfo_launcher/launcher_service.dart';
import 'package:openkfo_launcher/update_service.dart';
void main(){
 test('release is reported only after both manifests were verified at the same version',(){
  final u=UpdateService(LauncherService('.'));
  expect(()=>u.verifiedRelease,throwsException);
  u.verifiedLauncherVersion='oss.7';
  expect(()=>u.verifiedRelease,throwsException);
  u.verifiedClientVersion='oss.8';
  expect(()=>u.verifiedRelease,throwsException);
  u.verifiedClientVersion='oss.7';
  expect(u.verifiedRelease,'oss.7');
 });
}
