import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/buff_config.dart';

void main() {
  testWidgets('saving a new Buff sends the state edit request', (tester) async {
    tester.view.physicalSize = const Size(1600, 2200);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final calls = <Map<String, dynamic>>[];
    Future<dynamic> api(Map<String, dynamic> request) async {
      calls.add(request);
      switch (request['operation']) {
        case 'weapon_buff_catalog':
          return {'buffs': [], 'lua_entry': 'script/playereventproc/ustateeventproc.lua'};
        case 'weapon_buff_icons':
          return {'icons': []};
        case 'weapon_buff_api':
          return {'groups': [], 'events': [], 'fields': []};
        case 'weapon_buff_save':
          return {'message': '状态 435 已保存'};
        default:
          throw StateError('unexpected request ${request['operation']}');
      }
    }

    await tester.pumpWidget(
      MaterialApp(home: BuffConfigPage(api: api)),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.text('新建状态'));
    await tester.pump();
    await tester.enterText(find.widgetWithText(TextField, '状态号'), '435');
    await tester.tap(find.text('保存状态'));
    await tester.pumpAndSettle();

    final save = calls.singleWhere(
      (request) => request['operation'] == 'weapon_buff_save',
    );
    expect(save['key'], '435');
    expect((save['ustate'] as Map)['action'], 'upsert');
    expect(tester.takeException(), isNull);
  });
}
