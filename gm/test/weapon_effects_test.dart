import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/weapon_config.dart';

void main() {
  testWidgets(
    'effect repair previews and merges into memory without legacy writes',
    (tester) async {
      tester.view.physicalSize = const Size(1600, 1000);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final calls = <String>[];
      Map<String, dynamic>? savedWorkspace;
      final weapon = {
        'id': 253300,
        'name': '流氓拳',
        'type': '拳套',
        'description': '',
        'stages': <dynamic>[],
        'combos': <dynamic>[],
      };
      final common = {
        'revision': 'catalog',
        'fields': <dynamic>[],
        'effects': <dynamic>[],
        'hit_options': <String, dynamic>{},
        'drafts': <String, dynamic>{},
        'applied': <String, dynamic>{},
        'created': <String, dynamic>{},
        'states': <dynamic>[],
        'ustates': <dynamic>[],
        'buffs': <dynamic>[],
      };
      Future<dynamic> api(Map<String, dynamic> request) async {
        calls.add('${request['operation']}');
        switch (request['operation']) {
          case 'weapon_effect_view':
            return {
              'registered': [
                {'effect_id': '100', 'file': '100', 'thumbnail': ''},
              ],
              'references': <dynamic>[],
              'unregistered': <dynamic>[],
              'common_count': 1,
            };
          case 'weapon_workspace_save':
            savedWorkspace = Map<String, dynamic>.from(
              request['workspace'] as Map,
            );
            return {'message': '暂存已保存', 'saved_at': 'test'};
          case 'weapon_effects_preview':
            expect(request['weapon'], 253300);
            return {
              'revision': 'live-hash',
              'path': 'client/Data/config.spf2',
              'additions': [
                {'id': '183011', 'file': '183011'},
              ],
              'issues': ['特效 6001178 没有原始登记'],
            };
          case 'weapon_apply':
            return {'message': '已应用'};
          case 'weapon_list':
            return {
              ...common,
              'weapons': [weapon],
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
        }
        fail('Unexpected operation: ${request['operation']}');
      }

      await tester.pumpWidget(MaterialApp(home: WeaponConfigPage(api: api)));
      await tester.pump(const Duration(milliseconds: 100));
      expect(find.text('请从左侧选择武器查看配置'), findsOneWidget);
      await tester.tap(find.text('流氓拳'));
      await tester.pump(const Duration(milliseconds: 100));
      await tester.ensureVisible(find.text('自动补齐攻击特效'));
      await tester.tap(find.text('自动补齐攻击特效'));
      await tester.pump(const Duration(milliseconds: 100));
      expect(calls, contains('weapon_effects_preview'));
      expect(calls, isNot(contains('weapon_effects_apply')));
      expect(find.textContaining('6001178'), findsOneWidget);
      await tester.tap(find.text('关闭'));
      await tester.pump(const Duration(milliseconds: 100));
      expect(calls, isNot(contains('weapon_effects_apply')));
      await tester.tap(find.text('自动补齐攻击特效'));
      await tester.pump(const Duration(milliseconds: 100));
      await tester.tap(find.text('备份并补齐'));
      await tester.pump(const Duration(milliseconds: 100));
      expect(calls, isNot(contains('weapon_effects_apply')));
      final page = tester.state(find.byType(WeaponConfigPage)) as dynamic;
      expect(page.effectRows, [
        {'effect_id': '100', 'file': '100'},
        {'effect_id': '183011', 'file': '183011'},
      ]);
      await page.saveWorkspace();
      await tester.pump(const Duration(milliseconds: 100));
      expect(savedWorkspace!['effect_rows'], [
        {'effect_id': '100', 'file': '100'},
        {'effect_id': '183011', 'file': '183011'},
      ]);
    },
  );

  testWidgets(
    'effect ledger and stage effects preserve edits across reopen, workspace load, and apply',
    (tester) async {
      tester.view.physicalSize = const Size(3000, 1600);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final calls = <String>[];
      Map<String, dynamic>? savedWorkspace;
      final weapon = {
        'id': 253301,
        'name': '特效测试武器',
        'type': '拳套',
        'description': '',
        'stages': [
          {
            'state': 2011,
            'label': '普通攻击',
            'action': 1001,
            'property_ids': <dynamic>[],
          },
        ],
        'combos': <dynamic>[],
      };
      final common = {
        'revision': 'test-revision',
        'fields': <dynamic>[],
        'effects': <dynamic>[],
        'hit_options': <String, dynamic>{},
        'drafts': <String, dynamic>{},
        'applied': <String, dynamic>{},
        'created': {
          '253301': {'donor': 0},
        },
        'states': <dynamic>[],
        'ustates': <dynamic>[],
        'buffs': <dynamic>[],
      };
      final baselineStageEffect = {
        'state': '2011',
        'kind': 'hit',
        'effect_id': '100',
        'start': 1,
        'end': 3,
        'bind_type': '2',
        'bind_index': '4',
        'break': '0',
      };
      Future<dynamic> api(Map<String, dynamic> request) async {
        final operation = '${request['operation']}';
        calls.add(operation);
        switch (operation) {
          case 'weapon_effect_view':
            return {
              'registered': [
                {'effect_id': '100', 'file': '100', 'thumbnail': ''},
              ],
              'references': [baselineStageEffect],
              'unregistered': <dynamic>[],
              'common_count': 1,
            };
          case 'weapon_workspace_status':
            return {'exists': savedWorkspace != null};
          case 'weapon_workspace_save':
            savedWorkspace = Map<String, dynamic>.from(
              request['workspace'] as Map,
            );
            return {'message': '暂存已保存', 'saved_at': 'saved-at-test'};
          case 'weapon_workspace_load':
            return {
              'exists': savedWorkspace != null,
              'payload': savedWorkspace,
            };
          case 'weapon_apply':
            return {'message': '已应用'};
          case 'weapon_list':
            return {
              ...common,
              'weapons': [weapon],
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
        }
        fail('Unexpected operation: $operation');
      }

      await tester.pumpWidget(MaterialApp(home: WeaponConfigPage(api: api)));
      await tester.pump(const Duration(milliseconds: 100));
      await tester.tap(find.text('特效测试武器'));
      await tester.pump(const Duration(milliseconds: 100));

      final page = tester.state(find.byType(WeaponConfigPage)) as dynamic;
      page.openEffectEditor();
      await tester.pump(const Duration(milliseconds: 100));
      expect(find.text('100'), findsOneWidget);
      await tester.tap(find.text('关闭'));
      await tester.pump(const Duration(milliseconds: 100));
      expect(page.effectRows, isNull);
      expect(calls, isNot(contains('weapon_effect_ledger_set')));

      page.openEffectEditor();
      await tester.pump(const Duration(milliseconds: 100));
      await tester.tap(find.byTooltip('删除登记'));
      // Empty registration is a deliberate author override; the dialog can
      // save it without making any legacy ledger RPC.
      await tester.tap(find.text('保存登记'));
      await tester.pump(const Duration(milliseconds: 100));
      expect(page.effectRows, isEmpty);
      expect(calls, isNot(contains('weapon_effect_ledger_set')));

      page.openEffectEditor();
      await tester.pump(const Duration(milliseconds: 100));
      expect(find.text('这个武器还没有特效登记'), findsOneWidget);
      await tester.tap(find.text('关闭'));
      await tester.pump(const Duration(milliseconds: 100));

      page.openStageEffects(0);
      await tester.pump(const Duration(milliseconds: 100));
      expect(find.textContaining('命中特效 100'), findsOneWidget);
      await tester.tap(find.text('关闭').last);
      await tester.pump(const Duration(milliseconds: 100));
      expect(page.stageEffects, isEmpty);
      expect(calls, isNot(contains('weapon_effect_stage_set')));

      page.stageEffects = {
        '2011': [
          {
            'kind': 'hit',
            'effect_id': '6001178',
            'start': 2,
            'end': 8,
            'bind_type': '2',
            'bind_index': '29',
            'break': '1',
          },
        ],
      };
      page.openStageEffects(0);
      await tester.pump(const Duration(milliseconds: 100));
      expect(find.text('保存招式特效'), findsOneWidget);
      expect(find.textContaining('命中特效 6001178'), findsOneWidget);
      await tester.tap(find.text('关闭').last);
      await tester.pump(const Duration(milliseconds: 100));
      page.openStageEffects(0);
      await tester.pump(const Duration(milliseconds: 100));
      expect(find.textContaining('命中特效 6001178'), findsOneWidget);
      await tester.tap(find.text('保存招式特效'));
      await tester.pump(const Duration(milliseconds: 100));
      expect(page.stageEffects['2011'], [
        {
          'kind': 'hit',
          'effect_id': '6001178',
          'start': 2,
          'end': 8,
          'bind_type': '2',
          'bind_index': '29',
          'break': '1',
        },
      ]);
      expect(calls, isNot(contains('weapon_effect_stage_set')));

      await page.saveWorkspace();
      await tester.pump(const Duration(milliseconds: 100));
      expect(savedWorkspace!['effect_rows'], isEmpty);
      expect(
        savedWorkspace!['stage_effects']['2011'],
        equals([
          {
            'kind': 'hit',
            'effect_id': '6001178',
            'start': 2,
            'end': 8,
            'bind_type': '2',
            'bind_index': '29',
            'break': '1',
          },
        ]),
      );

      page.effectRows = [
        {'effect_id': 'changed', 'file': 'changed'},
      ];
      page.stageEffects = <String, List<Map<String, dynamic>>>{};
      await page.loadWorkspace();
      await tester.pump(const Duration(milliseconds: 100));
      expect(page.effectRows, isEmpty);
      expect(page.stageEffects['2011'], isNotEmpty);

      final applyCallsBeforeCancel = List<String>.from(calls);
      page.execute('weapon_apply');
      await tester.pump(const Duration(milliseconds: 100));
      await tester.tap(find.text('取消'));
      await tester.pump(const Duration(milliseconds: 100));
      expect(calls, applyCallsBeforeCancel);
      page.execute('weapon_apply');
      await tester.pump(const Duration(milliseconds: 100));
      await tester.tap(find.text('确认写入'));
      await tester.pump(const Duration(milliseconds: 100));
      expect(calls, isNot(contains('weapon_effect_ledger_set')));
      expect(calls, isNot(contains('weapon_effect_stage_set')));
      expect(calls, contains('weapon_apply'));
      expect(calls, contains('weapon_workspace_save'));
    },
  );
}
