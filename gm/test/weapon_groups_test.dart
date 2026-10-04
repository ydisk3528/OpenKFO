import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/weapon_config.dart';

void main() {
  testWidgets('combo groups share state edits and keep unmapped actions', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1400, 1200);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    Map<String, dynamic>? saved;
    Map<String, dynamic> stage(int id) => {
      'stage': id,
      'state': '$id',
      'label': '动作 $id',
      'action': '2001001',
      'property_ids': <String>[],
      'hits': <dynamic>[],
      'supported': true,
    };
    final weapon = {
      'id': 1,
      'name': '测试武器',
      'type': '测试',
      'description': '',
      'stages': [stage(2021), stage(999)],
      'combos': [
        {
          'name': '路线一',
          'nodes': [
            {'state': '2021', 'keys': 'CCCX'},
          ],
        },
        {
          'name': '路线二',
          'nodes': [
            {'state': '2021', 'keys': 'XX'},
          ],
        },
      ],
    };
    final common = {
      'revision': 'test',
      'fields': <dynamic>[],
      'effects': <dynamic>[],
      'hit_options': <String, dynamic>{},
      'drafts': {
        '1': [
          {'stage': 2021, 'buff': 14, 'level': 1, 'duration': 3000},
        ],
      },
      'applied': <String, dynamic>{},
      'created': <String, dynamic>{},
      'states': [2021, 999],
      'ustates': <dynamic>[],
      'buffs': [
        {'id': 0, 'name': '原效果'},
        {'id': 14, 'name': '冰冻'},
      ],
    };
    Future<dynamic> api(Map<String, dynamic> request) async {
      switch (request['operation']) {
        case 'weapon_list':
          return {
            ...common,
            'weapons': [
              {
                ...weapon,
                'stages': [
                  {'state': '2021', 'label': '动作 2021'},
                  {'state': '999', 'label': '动作 999'},
                ],
              },
            ],
          };
        case 'client_directory_get':
          return <String, dynamic>{};
        case 'weapon_detail':
          return {
            ...common,
            'weapon': weapon,
            'weapons': [weapon],
            'chain_info': <String, dynamic>{},
            'combo_rule_info': <String, dynamic>{},
          };
        case 'weapon_workspace_save':
          saved = Map<String, dynamic>.from(request['workspace'] as Map);
          return {'message': '已保存'};
      }
      fail('Unexpected operation: ${request['operation']}');
    }

    await tester.pumpWidget(MaterialApp(home: WeaponConfigPage(api: api)));
    await tester.pumpAndSettle();
    expect(find.text('该武器默认连招'), findsNothing);
    expect(find.text('请从左侧选择武器查看配置'), findsOneWidget);
    await tester.tap(find.text('测试武器'));
    await tester.pumpAndSettle();
    expect(find.text('动作说明（1）'), findsOneWidget);
    await tester.tap(find.text('CCCX'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('第 1 段 · CCCX'));
    await tester.pumpAndSettle();
    await tester.enterText(find.widgetWithText(TextFormField, '3000'), '5000');
    await tester.tap(find.text('CCCX'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('XX'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('第 1 段 · XX'));
    await tester.pumpAndSettle();
    expect(find.widgetWithText(TextFormField, '5000'), findsOneWidget);
    await tester.tap(find.text('保存方案'));
    await tester.pumpAndSettle();
    final rules = saved!['rules'] as List;
    expect(rules.where((r) => r['stage'] == 2021).single['duration'], 5000);
    expect(rules.where((r) => r['stage'] == 999).length, 1);
    expect(tester.takeException(), isNull);
  });
}
