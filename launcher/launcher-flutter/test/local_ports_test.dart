import 'dart:io';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:openkfo_launcher/launcher_service.dart';
import 'package:openkfo_launcher/main.dart';

class IdlePortService extends LauncherService {
  IdlePortService(super.root);
  @override
  Future<Map<String,dynamic>?> liveBridgeOwner() async => null;
  @override
  Future<Map<String,dynamic>?> state(int n) async => null;
}

void main() {
  test('busy TCP or UDP enables and persists automatic allocation', () async {
    final root=await Directory.systemTemp.createTemp('port-fallback-');
    addTearDown(()=>root.delete(recursive:true));
    final service=IdlePortService(root.path);
    final reserved=<ServerSocket>[];
    for(var n=0;n<3;n++) {
      reserved.add(await ServerSocket.bind(InternetAddress.loopbackIPv4,0));
    }
    service.localPorts=reserved.map((s)=>s.port).toList();
    await service.enableAutomaticPortsIfUnavailable();
    expect(service.autoPorts,isTrue);
    final restored=LauncherService(root.path);
    await restored.loadPortSettings();
    expect(restored.autoPorts,isTrue);
    for(final s in reserved) { await s.close(); }
    service.autoPorts=false;
    await service.enableAutomaticPortsIfUnavailable();
    expect(service.autoPorts,isFalse);
    final udp=await RawDatagramSocket.bind(InternetAddress.loopbackIPv4,service.localPorts[2],reuseAddress:false);
    try {
      await service.enableAutomaticPortsIfUnavailable();
      expect(service.autoPorts,isTrue);
    } finally { udp.close(); }
  });
  testWidgets('port settings shows defaults and automatic mode', (tester) async {
    tester.view.physicalSize=const Size(1280,900);
    tester.view.devicePixelRatio=1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(const MaterialApp(home:LauncherPage(preview:true)));
    final dynamic state=tester.state(find.byType(LauncherPage));
    final dialog=state.portSettings();
    await tester.pumpAndSettle();
    expect(find.text('38184'),findsOneWidget);
    expect(find.text('38180'),findsOneWidget);
    expect(find.text('38181'),findsOneWidget);
    await tester.tap(find.text('自动分配'));
    await tester.pumpAndSettle();
    final fields=tester.widgetList<TextField>(find.descendant(of:find.byType(AlertDialog),matching:find.byType(TextField)));
    expect(fields.every((f)=>f.enabled==false),isTrue);
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();
    await dialog;
    expect(tester.takeException(),isNull);
  });
  test('local ports defaults, validation and persisted automatic mode', () async {
    final root=await Directory.systemTemp.createTemp('port-settings-');
    addTearDown(()=>root.delete(recursive:true));
    final service=LauncherService(root.path);
    expect(service.localPorts,[38184,38180,38181]);
    expect(service.autoPorts,false);
    for(final ports in [[0,1,2],[1,1,2],[1,2,65536]]) {
      expect(()=>LauncherService.validatePorts(ports),throwsFormatException);
    }
    await service.portSettingsFile.writeAsString('{"ports":[39184,39180,39181],"automatic":true}');
    await service.loadPortSettings();
    expect(service.localPorts,[39184,39180,39181]);
    expect(service.autoPorts,true);
  });
}
