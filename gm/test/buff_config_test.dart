import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/buff_config.dart';

const _identity = {'active_profile': 'profile-a', 'source_hash': 'hash-a'};

Future<void> _mount(
  WidgetTester tester,
  Future<dynamic> Function(Map<String, dynamic>) api,
) async {
  tester.view.physicalSize = const Size(1600, 2200);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  await tester.pumpWidget(MaterialApp(home: BuffConfigPage(api: api)));
  await tester.pumpAndSettle();
}

Map<String, dynamic> _readResponse(Map<String, dynamic> request) {
  switch (request['operation']) {
    case 'weapon_buff_catalog':
      return {
        ..._identity,
        'buffs': [
          {'type': '435', 'name': '状态 A'},
          {'type': '436', 'name': '状态 B'},
        ],
        'lua_entry': 'script/playereventproc/ustateeventproc.lua',
      };
    case 'weapon_buff_icons':
      return {..._identity, 'icons': []};
    case 'weapon_buff_api':
      return {..._identity, 'groups': [], 'events': [], 'fields': []};
    default:
      throw StateError('unexpected request ${request['operation']}');
  }
}

TextEditingController _controller(WidgetTester tester, int index) => tester
    .widgetList<TextField>(find.byType(TextField))
    .elementAt(index)
    .controller!;

void main() {
  testWidgets('保存和应用携带 catalog 身份', (tester) async {
    final calls = <Map<String, dynamic>>[];
    Future<dynamic> api(Map<String, dynamic> request) async {
      calls.add(request);
      switch (request['operation']) {
        case 'weapon_buff_save':
        case 'weapon_buff_apply':
          return {..._identity, 'message': '已完成'};
        default:
          return _readResponse(request);
      }
    }

    await _mount(tester, api);
    await tester.tap(find.text('新建状态'));
    await tester.pump();
    await tester.enterText(find.widgetWithText(TextField, '状态号'), '435');
    await tester.tap(find.text('保存状态'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, '应用到客户端'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('应用'));
    await tester.pumpAndSettle();

    final save = calls.singleWhere(
      (request) => request['operation'] == 'weapon_buff_save',
    );
    expect(save['key'], '435');
    expect((save['ustate'] as Map)['action'], 'upsert');
    expect(
      calls.where((r) => r['operation'] == 'weapon_buff_apply'),
      hasLength(1),
    );
    expect(calls.first['operation'], 'weapon_buff_catalog');
    expect(calls.first.containsKey('active_profile'), isFalse);
    expect(calls.first.containsKey('source_hash'), isFalse);
    for (final request in calls.skip(1)) {
      expect(request['active_profile'], 'profile-a');
      expect(request['source_hash'], 'hash-a');
    }
    expect(tester.takeException(), isNull);
  });

  testWidgets('迟到详情不能覆盖新选择，新建也使旧详情失效', (tester) async {
    final details = <Completer<Map<String, dynamic>>>[];
    Future<dynamic> api(Map<String, dynamic> request) async {
      if (request['operation'] == 'weapon_buff_detail') {
        expect(request['active_profile'], 'profile-a');
        expect(request['source_hash'], 'hash-a');
        final pending = Completer<Map<String, dynamic>>();
        details.add(pending);
        return pending.future;
      }
      return _readResponse(request);
    }

    await _mount(tester, api);
    await tester.tap(find.text('状态 A'));
    await tester.pump();
    await tester.tap(find.text('状态 B'));
    await tester.pump();
    expect(details, hasLength(2));
    details[1].complete({
      ..._identity,
      'node': '<Data type="436" />',
      'lua': 'lua B',
      'note': '说明 B',
    });
    await tester.pumpAndSettle();
    details[0].complete({
      ..._identity,
      'node': '<Data type="435" />',
      'lua': 'lua A',
      'note': '说明 A',
    });
    await tester.pumpAndSettle();
    expect(_controller(tester, 0).text, '436');
    expect(_controller(tester, 1).text, '说明 B');
    expect(_controller(tester, 2).text, '<Data type="436" />');
    expect(_controller(tester, 3).text, 'lua B');
    expect(
      tester.widget<ListTile>(find.widgetWithText(ListTile, '状态 B')).selected,
      isTrue,
    );

    await tester.tap(find.text('状态 A'));
    await tester.pump();
    await tester.tap(find.text('新建状态'));
    await tester.pump();
    final template = _controller(tester, 2).text;
    details[2].complete({..._identity, 'node': '迟到 XML', 'lua': '迟到 lua'});
    await tester.pumpAndSettle();
    expect(_controller(tester, 0).text, isEmpty);
    expect(_controller(tester, 2).text, template);
    expect(
      tester.widget<ListTile>(find.widgetWithText(ListTile, '状态 A')).selected,
      isFalse,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('刷新身份清编辑器并阻止旧详情和应用弹窗提交', (tester) async {
    var identity = Map<String, String>.from(_identity);
    final calls = <Map<String, dynamic>>[];
    final detail = Completer<Map<String, dynamic>>();
    Future<dynamic> api(Map<String, dynamic> request) async {
      calls.add(request);
      switch (request['operation']) {
        case 'weapon_buff_catalog':
          return {..._readResponse(request), ...identity};
        case 'weapon_buff_detail':
          return detail.future;
        case 'weapon_buff_apply':
          return {...identity, 'message': '不应提交'};
        default:
          return {..._readResponse(request), ...identity};
      }
    }

    await _mount(tester, api);
    await tester.tap(find.text('状态 A'));
    await tester.pump();
    await tester.tap(find.widgetWithText(FilledButton, '应用到客户端'));
    await tester.pumpAndSettle();
    identity = {'active_profile': 'profile-b', 'source_hash': 'hash-b'};
    // 模态弹窗阻止真实点击，通过页面的刷新回调模拟外部刷新完成。
    final refreshButton = tester.widget<IconButton>(
      find.ancestor(
        of: find.byIcon(Icons.refresh),
        matching: find.byType(IconButton),
      ),
    );
    refreshButton.onPressed!();
    await tester.pumpAndSettle();
    expect(_controller(tester, 0).text, isEmpty);
    expect(_controller(tester, 1).text, isEmpty);
    expect(_controller(tester, 2).text, isEmpty);
    expect(_controller(tester, 3).text, isEmpty);
    expect(
      tester.widget<ListTile>(find.widgetWithText(ListTile, '状态 A')).selected,
      isFalse,
    );
    final catalogs = calls.where(
      (r) => r['operation'] == 'weapon_buff_catalog',
    );
    expect(catalogs, hasLength(2));
    expect(catalogs.last.containsKey('active_profile'), isFalse);
    expect(catalogs.last.containsKey('source_hash'), isFalse);
    detail.complete({..._identity, 'node': '旧 XML', 'lua': '旧 lua'});
    await tester.pumpAndSettle();
    expect(_controller(tester, 2).text, isEmpty);
    await tester.tap(find.text('应用'));
    await tester.pumpAndSettle();
    expect(calls.where((r) => r['operation'] == 'weapon_buff_apply'), isEmpty);
    final icons = calls.lastWhere((r) => r['operation'] == 'weapon_buff_icons');
    expect(icons['active_profile'], 'profile-b');
    expect(icons['source_hash'], 'hash-b');
    expect(tester.takeException(), isNull);
  });

  testWidgets('身份不匹配的回包被拒绝且不能覆盖 catalog 身份', (tester) async {
    final calls = <Map<String, dynamic>>[];
    Future<dynamic> api(Map<String, dynamic> request) async {
      calls.add(request);
      if (request['operation'] == 'weapon_buff_save') {
        return {
          'active_profile': 'profile-b',
          'source_hash': 'hash-b',
          'message': '错误成功',
        };
      }
      if (request['operation'] == 'weapon_buff_lua_save') {
        return {..._identity, 'message': 'lua 已保存'};
      }
      return _readResponse(request);
    }

    await _mount(tester, api);
    await tester.tap(find.text('新建状态'));
    await tester.pump();
    await tester.enterText(find.widgetWithText(TextField, '状态号'), '435');
    await tester.tap(find.text('保存状态'));
    await tester.pumpAndSettle();
    expect(find.textContaining('回包身份不匹配'), findsOneWidget);
    expect(find.text('错误成功'), findsNothing);
    expect(
      calls.where((r) => r['operation'] == 'weapon_buff_catalog'),
      hasLength(1),
    );
    await tester.tap(find.text('保存 lua'));
    await tester.pumpAndSettle();
    final lua = calls.last;
    expect(lua['operation'], 'weapon_buff_lua_save');
    expect(lua['active_profile'], 'profile-a');
    expect(lua['source_hash'], 'hash-a');
    expect(tester.takeException(), isNull);
  });
}
