import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/weapon_config.dart';
import 'package:kungfu_item_manager/weapon_workspace.dart';

void main() {
  test('weapon workspace round trips an isolated complete snapshot', () {
    final workspace = WeaponWorkspace.fromPage(
      weapon: {'id': 253450, 'name': '狂暴·紫金八面锤'},
      data: {
        'revision': 'r1',
        'weapons': [
          {'id': 253450, 'name': '狂暴·紫金八面锤'},
        ],
      },
      rules: [
        {
          'stage': 2011,
          'buff': 406,
          'properties': {
            '910000000': {'SkillDamage': 3.3},
          },
        },
      ],
      comboChain: [
        {'keys': '1', 'state': '2011'},
      ],
      comboDeadEnds: const [],
      frameSwitches: [
        {'state': '2011', 'keycode': 1},
      ],
      frameEdits: {
        '2011': [
          {'state': '2011', 'next': '2012'},
        ],
      },
      frameSaved: const {},
      counters: [
        {'state': '2011', 'trigger': 'hit'},
      ],
      counterEdits: const {},
      counterSaved: const {},
      blockElements: [
        {'state': '2011', 'tag': 'AddBuff', 'value': 406},
      ],
      blockElementsSaved: const {},
      blockElementsEdit: const {},
      variants: {
        '406': [
          {
            'condition': 406,
            'segments': [
              {'skillproid': '910000000', 'damage': '3.3'},
            ],
          },
        ],
      },
      variantsSaved: const {},
      variantsEdit: const {},
      variantBases: const {},
      variantOccupiedIDs: {'910000000'},
      stageTracks: {
        '2011': {'frames': 24},
      },
      scopeSaved: const {},
      comboRuleInfo: {'max_hits': 3},
      comboRuleMaxDraft: [
        {'skillproid': '910000000', 'max': 3},
      ],
      comboRuleBlackDraft: const [],
      comboRuleWhiteDraft: const [],
      extra: {
        'ustate_options': [
          {'id': '406', 'name': '巨神'},
        ],
      },
      remaps: {
        '253450': {
          '2011': {'action': '2001001', 'property_id': '800000001'},
        },
      },
      cleared: {
        '253450': {'2012': true},
      },
      extraProperties: {
        '800000001': {'template': '253521'},
      },
      effectRows: [
        {'effect_id': '6001178', 'file': '6001178'},
      ],
      stageEffects: {
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
        '2012': const [],
      },
    );

    final restored = WeaponWorkspace.fromJson(
      jsonDecode(jsonEncode(workspace.toJson())) as Map<String, dynamic>,
    );
    expect(restored.weaponID, 253450);
    expect(restored.remaps, workspace.remaps);
    expect(restored.cleared, workspace.cleared);
    expect(restored.extraProperties, workspace.extraProperties);
    (restored.remaps['253450'] as Map)['2011']['action'] = 'changed';
    expect(workspace.remaps['253450']['2011']['action'], '2001001');
    final legacy = WeaponWorkspace.fromJson({
      'weapon': {'id': 1},
      'data': {
        'remaps': {
          '1': {
            '2011': {'action': 'old'},
          },
        },
        'cleared': {
          '1': {'2012': true},
        },
        'extra_properties': {
          '800000001': {'template': '253521'},
        },
      },
    });
    expect(legacy.remaps['1']['2011']['action'], 'old');
    expect(legacy.cleared['1']['2012'], true);
    expect(legacy.extraProperties['800000001']['template'], '253521');
    expect(
      WeaponWorkspace.fromJson({
        ...legacy.toJson(),
        'remaps': <String, dynamic>{},
      }).remaps,
      isEmpty,
    );
    expect(
      restored.rules.single['properties']['910000000']['SkillDamage'],
      3.3,
    );
    expect(restored.variants['406']!.single['condition'], 406);
    expect(
      restored.variants['406']!.single['segments'].single['skillproid'],
      '910000000',
    );
    expect(restored.variantOccupiedIDs, {'910000000'});
    expect(restored.effectRows, [
      {'effect_id': '6001178', 'file': '6001178'},
    ]);
    expect(restored.stageEffects['2011'], [
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
    expect(restored.stageEffects['2012'], isEmpty);
    expect(workspace.toJson().containsKey('effect_rows'), isTrue);
    expect(
      WeaponWorkspace.fromJson({
        'weapon': {'id': 1},
        'data': {},
      }).toJson().containsKey('effect_rows'),
      isFalse,
    );

    (restored.weapon['name'] as String);
    (restored.rules.single['properties'] as Map)['910000000']['SkillDamage'] =
        9;
    expect(workspace.weapon['name'], '狂暴·紫金八面锤');
    expect(
      workspace.rules.single['properties']['910000000']['SkillDamage'],
      3.3,
    );
  });

  test('workspace shares hit properties and migrates legacy rules', () {
    final legacy = WeaponWorkspace.fromJson({
      'weapon': {'id': 1},
      'data': {},
      'rules': [
        {
          'stage': 1,
          'properties': {
            '910000001': {'SkillDamage': '3.3'},
          },
        },
      ],
    });
    expect(legacy.hitProperties['910000001'], {
      'id': '910000001',
      'values': {'SkillDamage': '3.3'},
    });

    final workspace = WeaponWorkspace.fromPage(
      weapon: {
        'id': 1,
        'stages': [
          {
            'state': '2011',
            'action': '77',
            'property_ids': ['910000001'],
            'hits': [
              {'id': '910000001'},
            ],
          },
        ],
      },
      data: {},
      rules: [
        {
          'stage': 1,
          'properties': {
            '910000001': {
              'id': '910000001',
              'values': {'SkillDamage': 3.3},
              'buff': '0',
              'variant': '406',
            },
          },
        },
      ],
      comboChain: const [],
      comboDeadEnds: const [],
      frameSwitches: const [],
      frameEdits: const {},
      frameSaved: const {},
      counters: const [],
      counterEdits: const {},
      counterSaved: const {},
      blockElements: const [],
      blockElementsSaved: const {},
      blockElementsEdit: const {},
      variants: const {},
      variantsSaved: const {},
      variantsEdit: const {},
      variantBases: const {},
      variantOccupiedIDs: const {},
      stageTracks: const {},
      scopeSaved: const {},
      comboRuleInfo: const {},
      comboRuleMaxDraft: const [],
      comboRuleBlackDraft: const [],
      comboRuleWhiteDraft: const [],
      hitProperties: {
        '910000001': {'SkillDamage': 4.5},
      },
    );
    expect(workspace.rules.single['properties']['910000001'], {
      'SkillDamage': 3.3,
    });
    expect(jsonEncode(workspace.rules), contains('"SkillDamage":3.3'));
    expect(jsonEncode(workspace.rules), isNot(contains('"id"')));
    expect(jsonEncode(workspace.rules), isNot(contains('"values"')));
    expect(jsonEncode(workspace.rules), isNot(contains('"buff"')));
    expect(jsonEncode(workspace.rules), isNot(contains('"variant"')));
    expect((workspace.toJson()['hit_properties'] as Map)['910000001'], {
      'id': '910000001',
      'action': '77',
      'owner_weapon': '1',
      'state': 2011,
      'values': {'SkillDamage': 4.5},
      'references': [
        {'weapon': '1', 'state': 2011, 'kind': 'stage'},
      ],
    });
    final restoredHit =
        (WeaponWorkspace.fromJson(workspace.toJson()).hitProperties)['910000001'];
    expect(restoredHit, {
      'id': '910000001',
      'action': '77',
      'owner_weapon': '1',
      'state': 2011,
      'values': {'SkillDamage': 4.5},
      'references': [
        {'weapon': '1', 'state': 2011, 'kind': 'stage'},
      ],
    });
    expect((restoredHit as Map)['id'], '910000001');
    expect((restoredHit['values'] as Map)['SkillDamage'], 4.5);
  });

  test('workspace variants normalize legacy string conditions to integers', () {
    final restored = WeaponWorkspace.fromJson({
      'weapon': {'id': 1},
      'variants': {
        '1': [
          {'condition': '406', 'segments': const []},
        ],
      },
    });
    expect(restored.variants['1']!.single['condition'], 406);
    expect(restored.toJson()['variants']['1'].single['condition'], 406);
  });

  test('workspace preserves variant deletion tombstones', () {
    final restored = WeaponWorkspace.fromJson({
      'weapon': {'id': 1},
      'variants': {
        '1': [
          {'condition': 1071, 'remove': true, 'segments': const []},
        ],
      },
    });
    final row = restored.toJson()['variants']['1'].single as Map;
    expect(row['condition'], 1071);
    expect(row['remove'], isTrue);
  });

  test('workspace variants reject invalid conditions', () {
    expect(
      () => WeaponWorkspace.fromJson({
        'weapon': {'id': 1},
        'variants': {
          '1': [
            {'condition': '406x', 'segments': const []},
          ],
        },
      }),
      throwsA(isA<FormatException>()),
    );
  });

  test('variant skillpro ids use the complete configured range', () {
    expect(isVariantSkillProID('910000000'), isTrue);
    expect(isVariantSkillProID('910999999'), isTrue);
    expect(isVariantSkillProID('910001000'), isTrue);
    expect(isVariantSkillProID('909999999'), isFalse);
    expect(isVariantSkillProID('911000000'), isFalse);
    expect(
      nextVariantSkillProID(
        {'910000000', '910000001'},
        minimum: 910000000,
        maximum: 910000003,
      ),
      '910000002',
    );
  });

  test('variant skillpro allocation stops at the configured upper bound', () {
    expect(
      nextVariantSkillProID(
        {'910000000', '910000001'},
        minimum: 910000000,
        maximum: 910000001,
      ),
      isNull,
    );
  });

  for (final scenario in ['draft', 'native', 'legacy', 'exhausted']) {
    testWidgets('variant hit editing real widget flow: $scenario', (
      tester,
    ) async {
      tester.view.physicalSize = const Size(1600, 1200);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final calls = <String>[];
      List<String> logicalCalls() =>
          calls.where((operation) => operation != 'shop_images').toList();
      List<Map<String, dynamic>>? savedRules;
      Map<String, dynamic>? savedVariants;
      final initialBranch = {
        'condition': 406,
        'segments': [
          {
            'name': 'base_attack',
            'start': 0,
            'end': 8,
            'skillproid': '253521',
            if (scenario == 'native') 'template_skillproid': '253522',
            'damage': '3.3',
            'replay_times': 2,
          },
        ],
      };
      final baseHit = {
        'id': '253521',
        'values': {
          'SkillDamage': scenario == 'draft' ? '5' : '3.3',
          'RepulseTarget': '0',
        },
        'buff': '0',
      };
      final stage = {
        'stage': 1,
        'state': '1',
        'label': 'C',
        'action': '2001001',
        'property_ids': ['253521'],
        'hits': [
          baseHit,
          if (scenario == 'native')
            {
              'id': '253522',
              'values': {'SkillDamage': '5', 'RepulseTarget': '0'},
              'buff': '0',
            },
        ],
        'supported': true,
        'reason': '',
      };
      Future<dynamic> api(Map<String, dynamic> request) async {
        final operation = '${request['operation']}';
        calls.add(operation);
        if (operation == 'weapon_list' || operation == 'weapon_detail') {
          final catalog = {
            'revision': 'variant-test-revision',
            'fields': [
              {'key': 'SkillDamage', 'name': '基础伤害', 'min': 0, 'max': 10000},
              {
                'key': 'RepulseTarget',
                'name': '击退',
                'min': 0,
                'max': 1,
                'oneshot': true,
              },
            ],
            'effects': <dynamic>[],
            'buffs': [
              {'id': 0, 'name': '保持原效果'},
            ],
            'hit_options': {
              'RepulseTarget': [
                {'value': 0, 'label': '不击退', 'detail': ''},
                {'value': 1, 'label': '击退', 'detail': ''},
              ],
            },
            'applied': {'253013': <dynamic>[]},
            'drafts': {
              '253013': scenario == 'legacy' || scenario == 'exhausted'
                  ? [
                      {
                        'stage': 1,
                        'buff': 0,
                        'level': 1,
                        'duration': 3000,
                        'properties': {
                          '253521': {'SkillDamage': 3.3, 'RepulseTarget': 1},
                        },
                      },
                    ]
                  : <dynamic>[],
            },
            'created': {
              '253013': {'donor': 0},
            },
            'states': [1],
            'ustates': [
              {'id': 406, 'name': '测试状态'},
            ],
            'variant_occupied_ids': ['910000000'],
            'variant_skillpro_min': 910000000,
            'variant_skillpro_max': scenario == 'exhausted'
                ? 910000000
                : 910000010,
            'weapons': [
              {
                'id': 253013,
                'name': '变体测试武器',
                'type': '测试',
                'description': '',
                'combos': [
                  {
                    'name': '站立攻击',
                    'nodes': [
                      {'keys': 'C', 'state': '1'},
                    ],
                  },
                ],
                'stages': [stage],
              },
            ],
          };
          if (operation == 'weapon_list') {
            return {
              ...catalog,
              'weapons': [
                for (final row in catalog['weapons'] as List)
                  {
                    ...row as Map,
                    'stages': [
                      {'state': '1', 'label': 'C'},
                    ],
                  },
              ],
            };
          }
          final detailWeapon = Map<String, dynamic>.from(
            (catalog['weapons'] as List).single as Map,
          );
          final chainInfo = {
            'chain': <dynamic>[],
            'dead_ends': <dynamic>[],
            'frame_switches': <dynamic>[],
            'frame_switches_saved': <String, dynamic>{},
            'frame_keys': <dynamic>[],
            'counters': <dynamic>[],
            'counters_saved': <String, dynamic>{},
            'block_elements': <dynamic>[],
            'block_elements_saved': <String, dynamic>{},
            'block_element_groups': <dynamic>[],
            'variants': {
              if (scenario == 'native') '1': [initialBranch],
            },
            'variants_saved': {
              if (scenario == 'legacy' || scenario == 'exhausted')
                '1': [initialBranch],
            },
            'variant_bases': {
              '1': [
                {
                  'name': 'base_attack',
                  'start': 0,
                  'end': 8,
                  'skillproid': '253521',
                  'damage': '3.3',
                  'replay_times': 2,
                },
              ],
            },
            'variant_occupied_ids': ['910000000'],
            'variant_skillpro_min': 910000000,
            'variant_skillpro_max': scenario == 'exhausted'
                ? 910000000
                : 910000010,
            'keys': <dynamic>[],
          };
          return {
            ...catalog,
            'weapon': detailWeapon,
            'weapons': [detailWeapon],
            'chain_info': chainInfo,
            'combo_rule_info': {'rules': {}, 'editable': true},
          };
        }
        if (operation == 'client_directory_get') return <String, dynamic>{};
        if (operation == 'weapon_combo_chain') {
          return {
            'chain': <dynamic>[],
            'dead_ends': <dynamic>[],
            'frame_switches': <dynamic>[],
            'frame_switches_saved': <String, dynamic>{},
            'frame_keys': <dynamic>[],
            'counters': <dynamic>[],
            'counters_saved': <String, dynamic>{},
            'block_elements': <dynamic>[],
            'block_elements_saved': <String, dynamic>{},
            'block_element_groups': <dynamic>[],
            'variants': {
              if (scenario == 'native') '1': [initialBranch],
            },
            'variants_saved': {
              if (scenario == 'legacy' || scenario == 'exhausted')
                '1': [initialBranch],
            },
            'variant_bases': {
              '1': [
                {
                  'name': 'base_attack',
                  'start': 0,
                  'end': 8,
                  'skillproid': '253521',
                  'damage': '3.3',
                  'replay_times': 2,
                },
              ],
            },
            'variant_occupied_ids': ['910000000'],
            'variant_skillpro_min': 910000000,
            'variant_skillpro_max': scenario == 'exhausted'
                ? 910000000
                : 910000010,
            'keys': <dynamic>[],
          };
        }
        if (operation == 'weapon_combo_rule') {
          return {'rules': {}, 'editable': true};
        }
        if (operation == 'weapon_variant_set') {
          savedVariants = Map<String, dynamic>.from(request['variants'] as Map);
          return {'message': '测试分支已保存'};
        }
        if (operation == 'weapon_save') {
          savedRules = [
            for (final rule in (request['rules'] as List))
              Map<String, dynamic>.from(rule as Map),
          ];
          return {'message': '方案已保存，尚未应用到游戏'};
        }
        fail('Unexpected operation: $operation');
      }

      await tester.pumpWidget(MaterialApp(home: WeaponConfigPage(api: api)));
      await tester.pumpAndSettle();
      expect(logicalCalls(), ['weapon_list', 'client_directory_get']);
      expect(find.text('请从左侧选择武器查看配置'), findsOneWidget);
      await tester.tap(find.text('变体测试武器'));
      await tester.pumpAndSettle();
      expect(logicalCalls(), [
        'weapon_list',
        'client_directory_get',
        'weapon_detail',
      ]);
      final dynamic page = tester.state(find.byType(WeaponConfigPage));
      final originalRules = [
        for (final rule in page.rules as List)
          {
            ...rule as Map,
            if (rule['properties'] is Map)
              'properties': {
                for (final entry in (rule['properties'] as Map).entries)
                  entry.key: Map<String, dynamic>.from(entry.value as Map),
              },
          },
      ];
      final originalDirty = page.dirty;
      final variantCard = find.byKey(const ValueKey('weapon-variants'));
      await tester.tap(
        find.descendant(
          of: variantCard,
          matching: find.widgetWithText(
            TextButton,
            scenario == 'draft' ? '添加' : '编辑',
          ),
        ),
      );
      await tester.pumpAndSettle();
      if (scenario != 'draft') {
        await tester.tap(
          find
              .descendant(of: variantCard, matching: find.byIcon(Icons.edit))
              .last,
        );
        await tester.pumpAndSettle();
        final id = scenario == 'legacy' ? '910000001' : '253521';
        expect(find.textContaining('命中属性 $id'), findsWidgets);
        if (scenario == 'exhausted') {
          await tester.tap(find.text('确定').last);
          await tester.pumpAndSettle();
          expect(find.textContaining('无法转换旧分支'), findsOneWidget);
          expect(find.text('动作分支 · 1 · C'), findsOneWidget);
          await tester.tap(find.text('取消').last);
        } else {
          await tester.tap(find.byType(ActionChip).last);
          await tester.pumpAndSettle();
          expect(find.text('命中属性 $id · 分支 406'), findsOneWidget);
          expect(
            find.descendant(
              of: find.byType(AlertDialog).last,
              matching: find.text('默认 ${scenario == 'draft' ? 5 : 3.3}'),
            ),
            findsOneWidget,
          );
          if (scenario == 'legacy') {
            await tester.tap(find.text('击飞参数 / 高级设置'));
            await tester.pumpAndSettle();
            expect(
              tester
                  .state<FormFieldState<int>>(
                    find.byType(DropdownButtonFormField<int>).last,
                  )
                  .value,
              1,
            );
          }
          await tester.tap(find.text('确定').last);
          await tester.pumpAndSettle();
          await tester.tap(find.text('确定').last);
          await tester.pumpAndSettle();
          final segment = page.variantsEdit['1'][0]['segments'][0] as Map;
          expect(segment['skillproid'], id);
          expect(segment['damage'], 3.3);
          if (scenario == 'legacy') {
            expect(page.rules[0]['properties'][id]['RepulseTarget'], 1);
          }
          await tester.tap(
            find.descendant(
              of: variantCard,
              matching: find.widgetWithText(FilledButton, '保存'),
            ),
          );
          await tester.pumpAndSettle();
          expect(page.variants['1'][0]['segments'][0]['skillproid'], id);
          expect(page.variants['1'][0]['segments'][0]['damage'], 3.3);
          expect(calls, isNot(contains('weapon_variant_set')));
          expect(calls, isNot(contains('weapon_save')));
        }
        expect(tester.takeException(), isNull);
        return;
      }
      // 模拟父编辑器另一个状态尚未保存的本地预占，不混入服务端占用。
      page.variantPut('2', 406, <Map<String, dynamic>>[
        {
          'name': 'other_draft',
          'start': 0,
          'end': 8,
          'skillproid': '910000001',
          'template_skillproid': '253521',
        },
      ]);
      await tester.pumpAndSettle();
      await tester.tap(find.text('给某个状态添加'));
      await tester.pumpAndSettle();
      expect(find.text('为哪个状态添加动作分支'), findsOneWidget);
      await tester.tap(find.text('1 · C'));
      await tester.pumpAndSettle();
      expect(find.text('动作分支 · 1 · C'), findsOneWidget);
      await tester.tap(find.byType(DropdownButtonFormField<String>).last);
      await tester.pumpAndSettle();
      await tester.tap(find.text('406（测试状态）'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('添加一段（默认卡帧）'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.widgetWithText(TextFormField, '动画名').last,
        'branch_attack',
      );
      await tester.enterText(find.widgetWithText(TextFormField, '起').last, '0');
      await tester.enterText(find.widgetWithText(TextFormField, '止').last, '8');
      await tester.tap(find.text('增加命中属性'));
      await tester.pumpAndSettle();
      expect(find.text('命中属性 910000002 · 分支 406'), findsOneWidget);
      expect(find.text('选择命中属性模板（按此复制新节点）'), findsNothing);
      expect(
        calls.where((operation) => operation == 'weapon_remap_options'),
        isEmpty,
      );
      await tester.enterText(
        find.widgetWithText(TextFormField, '基础伤害').last,
        '3.3',
      );
      await tester.tap(find.text('击飞参数 / 高级设置'));
      await tester.pumpAndSettle();
      await tester.tap(find.byType(DropdownButtonFormField<int>).last);
      await tester.pumpAndSettle();
      await tester.tap(find.textContaining('击退').last);
      await tester.pumpAndSettle();
      await tester.tap(find.text('确定').last);
      await tester.pumpAndSettle();
      expect(find.text('命中属性 910000002（待应用）'), findsOneWidget);
      await tester.ensureVisible(find.text('命中属性 910000002（待应用）'));
      await tester.tap(find.text('命中属性 910000002（待应用）'));
      await tester.pumpAndSettle();
      expect(find.widgetWithText(TextFormField, '基础伤害'), findsOneWidget);
      await tester.tap(find.text('击飞参数 / 高级设置'));
      await tester.pumpAndSettle();
      final reopenedAdvanced = find.byType(DropdownButtonFormField<int>).last;
      expect(tester.state<FormFieldState<int>>(reopenedAdvanced).value, 1);
      await tester.tap(find.text('确定').last);
      await tester.pumpAndSettle();
      await tester.tap(find.text('确定').last);
      await tester.pumpAndSettle();
      expect(page.rules[0]['properties']['910000002'], {
        'SkillDamage': 3.3,
        'RepulseTarget': 1,
      });
      expect(
        page.variantsEdit['1'][0]['segments'][0]['skillproid'],
        '910000002',
      );
      await tester.tap(find.text('C').first);
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.text('第 1 段 · C'));
      await tester.tap(find.text('第 1 段 · C'));
      await tester.pumpAndSettle();
      expect(find.text('分支 406（测试状态）'), findsOneWidget);
      expect(find.text('命中属性 910000002'), findsOneWidget);
      final draftDamage = find.byKey(
        ValueKey('${page.editorVersion}-1-910000002-SkillDamage'),
      );
      expect(draftDamage, findsOneWidget);
      expect(tester.widget<TextFormField>(draftDamage).initialValue, '3.3');
      expect(
        tester
            .widget<TextField>(
              find.descendant(
                of: draftDamage,
                matching: find.byType(TextField),
              ),
            )
            .decoration!
            .helperText,
        '默认 5',
      );

      Future<void> reopenBranch() async {
        final summary = find.textContaining('命中属性910000002');
        await tester.ensureVisible(summary);
        final row = find
            .ancestor(of: summary, matching: find.byType(Row))
            .first;
        await tester.tap(
          find.descendant(of: row, matching: find.byIcon(Icons.edit)),
        );
        await tester.pumpAndSettle();
      }

      await reopenBranch();
      await tester.tap(find.byType(ActionChip).last);
      await tester.pumpAndSettle();
      expect(find.text('命中属性 910000002 · 分支 406'), findsOneWidget);
      await tester.tap(find.text('击飞参数 / 高级设置').last);
      await tester.pumpAndSettle();
      expect(
        tester
            .state<FormFieldState<int>>(
              find.byType(DropdownButtonFormField<int>).last,
            )
            .value,
        1,
      );
      await tester.tap(find.text('清除全部改动'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('确定').last);
      await tester.pumpAndSettle();
      expect(page.hitProperties.containsKey('910000002'), isFalse);
      expect(
        (page.rules[0]['properties'] as Map?)?.containsKey('910000002') ??
            false,
        isFalse,
      );
      await reopenBranch();
      await tester.tap(find.text('清除').last);
      await tester.pumpAndSettle();
      expect(
        tester
            .widget<TextFormField>(find.widgetWithText(TextFormField, '卡帧'))
            .controller!
            .text,
        '2',
      );
      await tester.tap(find.text('增加命中属性'));
      await tester.pumpAndSettle();
      expect(find.text('命中属性 910000002 · 分支 406'), findsOneWidget);
      await tester.enterText(
        find.widgetWithText(TextFormField, '基础伤害').last,
        '0',
      );
      await tester.tap(find.text('确定').last);
      await tester.pumpAndSettle();
      await tester.tap(find.text('确定').last);
      await tester.pumpAndSettle();
      expect(page.rules[0]['properties']['910000002']['SkillDamage'], 0);
      await reopenBranch();
      await tester.tap(find.text('清除').last);
      await tester.pumpAndSettle();
      await tester.tap(find.text('确定').last);
      await tester.pumpAndSettle();
      expect(
        (page.rules[0]['properties'] as Map?)?.containsKey('910000002') ??
            false,
        isFalse,
      );
      await tester.ensureVisible(find.widgetWithText(TextButton, '取消').first);
      await tester.tap(find.widgetWithText(TextButton, '取消').first);
      await tester.pumpAndSettle();
      expect(page.rules, originalRules);
      expect(page.dirty, originalDirty);
      expect(page.localVariantIDs, isEmpty);
      expect(savedRules, isNull);

      // 第二轮通过真实 UI 增加、删除命中段，再验证默认卡帧保存 payload。
      await tester.tap(find.widgetWithText(TextButton, '添加').last);
      await tester.pumpAndSettle();
      await tester.tap(find.text('给某个状态添加'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('1 · C'));
      await tester.pumpAndSettle();
      await tester.tap(find.byType(DropdownButtonFormField<String>).last);
      await tester.pumpAndSettle();
      await tester.tap(find.text('406（测试状态）'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('照抄本招'));
      await tester.pumpAndSettle();
      await tester.tap(find.byType(ActionChip).last);
      await tester.pumpAndSettle();
      expect(
        find.descendant(
          of: find.byType(AlertDialog).last,
          matching: find.text('默认 ${scenario == 'draft' ? 5 : 3.3}'),
        ),
        findsOneWidget,
      );
      await tester.enterText(
        find.widgetWithText(TextFormField, '基础伤害').last,
        '3.3',
      );
      await tester.tap(find.text('确定').last);
      await tester.pumpAndSettle();
      await tester.tap(find.text('确定').last);
      await tester.pumpAndSettle();
      expect(page.rules[0]['properties']['910000001']['SkillDamage'], 3.3);
      final summary = find.textContaining('命中属性910000001');
      await tester.ensureVisible(summary);
      final row = find.ancestor(of: summary, matching: find.byType(Row)).first;
      await tester.tap(
        find.descendant(of: row, matching: find.byIcon(Icons.edit)),
      );
      await tester.pumpAndSettle();
      await tester.tap(
        find.descendant(
          of: find.byType(AlertDialog),
          matching: find.byIcon(Icons.close),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.text('添加一段（默认卡帧）'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.widgetWithText(TextFormField, '动画名').last,
        'hold_only',
      );
      await tester.enterText(find.widgetWithText(TextFormField, '止').last, '2');
      await tester.enterText(
        find.widgetWithText(TextFormField, '卡帧').last,
        '-1',
      );
      await tester.tap(find.text('确定').last);
      await tester.pumpAndSettle();
      expect(find.textContaining('卡帧必须是大于等于 0 的整数'), findsOneWidget);
      await tester.enterText(
        find.widgetWithText(TextFormField, '卡帧').last,
        'abc',
      );
      await tester.tap(find.text('确定').last);
      await tester.pumpAndSettle();
      expect(find.textContaining('卡帧必须是大于等于 0 的整数'), findsOneWidget);
      await tester.enterText(
        find.widgetWithText(TextFormField, '卡帧').last,
        '2',
      );
      await tester.tap(find.text('确定').last);
      await tester.pumpAndSettle();
      expect(
        (page.rules[0]['properties'] as Map?)?.containsKey('910000001') ??
            false,
        isFalse,
      );
      await tester.ensureVisible(find.widgetWithText(FilledButton, '保存'));
      await tester.tap(find.widgetWithText(FilledButton, '保存'));
      await tester.pumpAndSettle();
      final savedSegment = page.variants['1'][0]['segments'][0] as Map;
      expect(savedSegment['name'], 'hold_only');
      expect(savedSegment['replay_times'], 2);
      expect(savedSegment.containsKey('skillproid'), isFalse);
      expect(
        (page.rules[0]['properties'] as Map?)?.containsKey('910000001') ??
            false,
        isFalse,
      );
      expect(calls, isNot(contains('weapon_variant_set')));
      expect(calls, isNot(contains('weapon_save')));
      expect(
        calls.where((operation) => operation == 'weapon_remap_options'),
        isEmpty,
      );
      expect(calls.where((operation) => operation == 'weapon_apply'), isEmpty);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('253011 branch save preserves base and inherited hit values', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1600, 1200);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final base = <String, dynamic>{
      'SkillDamage': '8',
      'RepulseTarget': '1',
      'StandHurt': '42',
    };
    final baseline = <String, dynamic>{
      'id': 253011,
      'name': '属性保留测试',
      'type': '测试',
      'combos': <dynamic>[],
      'stages': [
        {
          'stage': 2041,
          'state': '2041',
          'label': '2041',
          'action': '2001011041',
          'property_ids': ['900000533', '900000534'],
          'hits': [
            {'id': '900000533', 'values': base, 'buff': '0'},
            {
              'id': '900000534',
              'values': {'SkillDamage': '12', 'RepulseTarget': '0'},
              'buff': '0',
            },
          ],
          'supported': true,
          'reason': '',
        },
      ],
    };
    Future<dynamic> api(Map<String, dynamic> request) async {
      switch (request['operation']) {
        case 'weapon_list':
          return {
            'weapons': [baseline],
            'fields': [
              {'key': 'SkillDamage', 'name': '基础伤害', 'min': 0, 'max': 10000},
              {'key': 'RepulseTarget', 'name': '击退', 'min': 0, 'max': 1, 'oneshot': true},
              {'key': 'StandHurt', 'name': '受击动作号', 'min': 0, 'max': 9999, 'oneshot': true},
            ],
            'effects': <dynamic>[],
            'hit_options': <String, dynamic>{},
            'buffs': <dynamic>[],
            'drafts': <String, dynamic>{},
            'applied': <String, dynamic>{},
            'created': <String, dynamic>{},
          };
        case 'weapon_detail':
          return {
            'weapon': jsonDecode(jsonEncode(baseline)),
            'drafts': <String, dynamic>{},
            // A canonical edit is sparse; it must not replace archive values.
            'hit_properties': {'900000533': {'values': {'RepulseTarget': 1}}},
            'ustates': [{'id': 406, 'name': '测试状态'}],
            'chain_info': {
              'variant_bases': {
                '2041': [
                  {'name': 'first_hit', 'start': 0, 'end': 8, 'skillproid': '900000533', 'damage': 8},
                  {'name': 'second_hit', 'start': 9, 'end': 16, 'skillproid': '900000534', 'damage': 12},
                ],
              },
              'variant_skillpro_min': 910000106,
              'variant_skillpro_max': 910000120,
            },
            'combo_rule_info': <String, dynamic>{},
          };
        case 'client_directory_get':
        case 'shop_images':
          return <String, dynamic>{};
        default:
          fail('Unexpected RPC: ${request['operation']}');
      }
    }
    await tester.pumpWidget(MaterialApp(home: WeaponConfigPage(api: api)));
    await tester.pumpAndSettle();
    final dynamic page = tester.state(find.byType(WeaponConfigPage));
    await page.select(baseline);
    await tester.pumpAndSettle();
    page.startVariantEdit();
    final opening = page.variantAddFor('2041');
    await tester.pumpAndSettle();
    await tester.tap(find.byType(DropdownButtonFormField<String>).last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('406（测试状态）'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('添加一段（默认卡帧）'));
    await tester.pumpAndSettle();
    await tester.enterText(find.widgetWithText(TextFormField, '动画名').last, 'new_hit');
    await tester.enterText(find.widgetWithText(TextFormField, '止').last, '8');
    await tester.tap(find.text('增加命中属性').last);
    await tester.pumpAndSettle();
    expect(find.text('命中属性 910000106 · 分支 406'), findsOneWidget);
    final damageField = find.widgetWithText(TextFormField, '基础伤害').last;
    expect(tester.widget<TextFormField>(damageField).controller!.text, '8');
    await tester.enterText(damageField, '0');
    await tester.tap(find.text('确定').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('确定').last);
    await opening;
    await tester.pumpAndSettle();
    await page.saveVariants();
    await tester.pumpAndSettle();
    final hits = page.weapon['stages'][0]['hits'] as List;
    expect((hits.firstWhere((hit) => hit['id'] == '900000533') as Map)['values'], {...base, 'RepulseTarget': 1});
    expect(page.hitProperties['910000106']['SkillDamage'], 0);
    expect(page.hitProperties['910000106']['RepulseTarget'], 1);
    expect(page.hitProperties['910000106']['StandHurt'], '42');
    expect(page.variants['2041'][0]['segments'][0]['damage'], 0);
    final restored = WeaponWorkspace.fromJson(page.workspace.toJson());
    expect(restored.hitProperties['900000533']['values']['SkillDamage'], 8);
    expect(restored.hitProperties['900000533']['values']['StandHurt'], 42);
    expect(restored.hitProperties['910000106']['values']['SkillDamage'], 0);
    expect(restored.hitProperties['910000106']['values']['StandHurt'], 42);
    page.startVariantEdit();
    final reopening = page.variantEditRow('2041', Map<String, dynamic>.from(page.variants['2041'][0] as Map));
    await tester.pumpAndSettle();
    await tester.tap(find.byType(ActionChip).last);
    await tester.pumpAndSettle();
    expect(tester.widget<TextFormField>(find.widgetWithText(TextFormField, '基础伤害').last).controller!.text, '0');
    await tester.tap(find.text('取消').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('取消').last);
    await reopening;
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
  });

  testWidgets('selection generation ignores an older detail response', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1600, 1200);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final first = Completer<dynamic>();
    final second = Completer<dynamic>();
    final calls = <String>[];
    Map<String, dynamic> weapon(int id, String name) => {
      'id': id,
      'name': name,
      'type': '测试',
      'description': '',
      'combos': <dynamic>[],
      'stages': [
        {
          'stage': 1,
          'state': '1',
          'label': 'C',
          'action': '2001001',
          'property_ids': <String>[],
          'hits': <dynamic>[],
          'supported': true,
          'reason': '',
        },
      ],
    };
    Map<String, dynamic> detail(Map<String, dynamic> value) => {
      'revision': 'generation-test',
      'weapon': value,
      'weapons': [value],
      'fields': <dynamic>[],
      'effects': <dynamic>[],
      'hit_options': <String, dynamic>{},
      'buffs': <dynamic>[],
      'applied': <String, dynamic>{},
      'drafts': <String, dynamic>{'${value['id']}': <dynamic>[]},
      'created': <String, dynamic>{},
      'states': [1],
      'ustates': <dynamic>[],
      'chain_info': {
        'chain': <dynamic>[],
        'dead_ends': <dynamic>[],
        'frame_switches': <dynamic>[],
        'frame_switches_saved': <String, dynamic>{},
        'frame_keys': <dynamic>[],
        'counters': <dynamic>[],
        'counters_saved': <String, dynamic>{},
        'block_elements': <dynamic>[],
        'block_elements_saved': <String, dynamic>{},
        'block_element_groups': <dynamic>[],
        'variants': <String, dynamic>{},
        'variants_saved': <String, dynamic>{},
        'variant_bases': <String, dynamic>{},
        'variant_occupied_ids': <dynamic>[],
        'variant_skillpro_min': 910000000,
        'variant_skillpro_max': 910000010,
        'keys': <dynamic>[],
      },
      'combo_rule_info': {'rules': {}, 'editable': true},
    };
    final one = weapon(1, '第一把');
    final two = weapon(2, '第二把');
    Future<dynamic> api(Map<String, dynamic> request) async {
      final operation = '${request['operation']}';
      calls.add(operation);
      if (operation == 'weapon_list') {
        return {
          'revision': 'generation-test',
          'weapons': [one, two],
          'fields': <dynamic>[],
          'effects': <dynamic>[],
          'hit_options': <String, dynamic>{},
          'buffs': <dynamic>[],
          'applied': <String, dynamic>{},
          'drafts': <String, dynamic>{},
          'created': <String, dynamic>{},
          'states': [1],
          'ustates': <dynamic>[],
          'undeployed': <dynamic>[],
        };
      }
      if (operation == 'client_directory_get') return <String, dynamic>{};
      if (operation == 'weapon_detail') {
        return request['weapon'] == 1 ? first.future : second.future;
      }
      fail('Unexpected operation: $operation');
    }

    await tester.pumpWidget(MaterialApp(home: WeaponConfigPage(api: api)));
    await tester.pumpAndSettle();
    final dynamic page = tester.state(find.byType(WeaponConfigPage));
    final selectingFirst = page.select(one);
    await tester.pump();
    final selectingSecond = page.select(two);
    second.complete(detail(two));
    await selectingSecond;
    await tester.pumpAndSettle();
    first.complete(detail(one));
    await selectingFirst;
    await tester.pumpAndSettle();
    expect(page.weapon['id'], 2);
    expect(page.weapon['name'], '第二把');
    expect(
      calls.where((operation) => operation == 'weapon_detail'),
      hasLength(2),
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'structure edits preserve baseline and complete workspace lifecycle',
    (tester) async {
      tester.view.physicalSize = const Size(1600, 1200);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final calls = <String>[];
      Map<String, dynamic>? saved, applied;
      final baseline = <String, dynamic>{
        'id': 1,
        'name': '结构测试',
        'type': '测试',
        'combos': <dynamic>[],
        'stages': [
          {
            'stage': 1,
            'state': '2011',
            'label': 'C',
            'action': '2001001',
            'property_ids': ['101'],
            'hits': <dynamic>[],
            'supported': true,
            'reason': '',
          },
          {
            'stage': 2,
            'state': '2012',
            'label': 'CC',
            'action': '2001002',
            'property_ids': ['102'],
            'hits': <dynamic>[],
            'supported': true,
            'reason': '',
          },
        ],
      };
      Future<dynamic> api(Map<String, dynamic> r) async {
        final op = '${r['operation']}';
        calls.add(op);
        if (op == 'client_directory_get' || op == 'shop_images')
          return <String, dynamic>{};
        if (op == 'weapon_list')
          return {
            'revision': 'baseline',
            'weapons': [baseline],
            'fields': <dynamic>[],
            'buffs': <dynamic>[],
            'effects': <dynamic>[],
            'hit_options': <String, dynamic>{},
            'drafts': <String, dynamic>{},
            'applied': <String, dynamic>{},
            'created': <String, dynamic>{},
          };
        if (op == 'weapon_detail')
          return {
            'weapon': jsonDecode(jsonEncode(baseline)),
            'drafts': <String, dynamic>{},
            'remaps': {
              '1': {
                '2012': {'action': '2001002'},
              },
            },
            'cleared': {
              '1': {'2013': true},
            },
            'extra_properties': {
              '800000001': {'template': '101'},
            },
            'hit_properties': {
              '101': {
                'id': '101',
                'values': {'SkillDamage': '2.5', 'RepulseTarget': '0'},
                'buff': '0',
              },
            },
            'chain_info': <String, dynamic>{},
            'combo_rule_info': <String, dynamic>{},
          };
        if (op == 'weapon_workspace_save') {
          saved = Map<String, dynamic>.from(
            jsonDecode(jsonEncode(r['workspace'])) as Map,
          );
          return {'message': 'saved', 'saved_at': 'now'};
        }
        if (op == 'weapon_workspace_load')
          return {'exists': true, 'payload': saved};
        if (op == 'weapon_apply') {
          applied = Map<String, dynamic>.from(r['workspace'] as Map);
          return {'message': 'applied'};
        }
        fail('Unexpected RPC: $op');
      }

      await tester.pumpWidget(MaterialApp(home: WeaponConfigPage(api: api)));
      await tester.pumpAndSettle();
      final dynamic page = tester.state(find.byType(WeaponConfigPage));
      await page.select(baseline);
      await tester.pumpAndSettle();
      expect(page.remapFor('2012')['action'], '2001002');
      expect(page.cleared['1']['2013'], true);
      expect(page.hitProperties['101']['SkillDamage'], '2.5');
      final id = page.allocateProperty('101', <Map<String, dynamic>>[
        {'id': '800000002'},
      ]);
      expect(id, '800000003');
      expect(page.extraProperties[id]['owner_weapon'], '1');
      page.variantOccupiedIDs = <String>{'800000004'};
      final nextID = page.allocateProperty('101', <Map<String, dynamic>>[
        {'id': '800000002'},
      ]);
      expect(nextID, '800000005');
      page.rules[0]['properties'] = {
        '101': {'SkillDamage': 9},
      };
      page.rules[0]['buff'] = 406;
      page.frameSaved['2011'] = [
        {'attrs': <dynamic>[], 'frame': 'kept'},
      ];
      page.commitRemap('2011', <String, dynamic>{
        'action': '2002001',
        'property_id': id,
        'template_property_id': '101',
        'template_stage_data': {'frames': [1, 2, 3], 'raw_frames': '<Frames/>'},
      });
      expect(page.weapon['stages'][0]['action'], '2002001');
      expect(page.weapon['stages'][0]['property_ids'], ['101', id]);
      expect(page.weapon['stages'][0]['frames'], [1, 2, 3]);
      expect(page.weapon['stages'][0]['hits'].last['id'], id);
      expect(page.hitProperties[id]['SkillDamage'], '2.5');
      expect(page.frameSaved['2011'].single['frame'], 'kept');
      expect(page.rules[0]['properties'], isEmpty);
      expect(page.rules[0]['buff'], 0);
      expect(page.data['weapon']['stages'][0]['action'], '2001001');
      await page.saveWorkspace();
      expect(saved!['remaps']['1']['2011']['property_id'], id);
      expect(saved!['extra_properties'][id]['template'], '101');
      await page.select(baseline);
      await tester.pumpAndSettle();
      expect(page.remapFor('2011'), isEmpty);
      expect(page.extraProperties.containsKey(id), false);
      await page.loadWorkspace();
      await tester.pumpAndSettle();
      expect(page.remapFor('2011')['property_id'], id);
      await page.clearRemap('2011');
      expect(page.weapon['stages'][0]['action'], '2001001');
      page.addStatePick = '2013';
      final defining = page.defineState();
      await tester.pumpAndSettle();
      await tester.enterText(
        find.widgetWithText(TextField, '动作 ID'),
        '2003001',
      );
      await tester.enterText(find.widgetWithText(TextField, '命中属性 ID'), id);
      await tester.tap(find.text('提交重映射'));
      await tester.pumpAndSettle();
      await defining;
      expect(page.cleared['1'].containsKey('2013'), false);
      expect(page.rules.last['stage'], 3);
      page.comboChain = <Map<String, String>>[
        {'old': '2011', 'new': '2013', 'key': '1'},
        {'old': '2011', 'new': '2012', 'key': '2'},
      ];
      page.frameSaved = <String, List<Map<String, dynamic>>>{
        '2013': [
          {'attrs': <dynamic>[]},
        ],
        '2011': [
          {
            'attrs': [
              {'key': 'nextstate', 'value': '2013'},
            ],
          },
        ],
      };
      page.counterSaved = <String, Map<String, dynamic>?>{
        '2012': {
          'attrs': [
            {'key': 'nextstate', 'value': '2013'},
          ],
        },
      };
      page.stageEffects = <String, List<Map<String, dynamic>>>{
        '2013': [
          {'effect_id': '1'},
        ],
      };
      page.comboRuleBlackDraft = <Map<String, dynamic>>[
        {'prev': id, 'cur': '102'},
        {'prev': '101', 'cur': '102'},
      ];
      page.comboRuleInfo = <String, dynamic>{
        'rules': {
          'black': [
            {'prev': id, 'cur': '102'},
            {'prev': '101', 'cur': '102'},
          ],
        },
      };
      final deletion = page.clearState('2013');
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(FilledButton, '删除状态'));
      await tester.pumpAndSettle();
      await deletion;
      expect(page.cleared['1']['2013'], true);
      expect(page.remapFor('2013'), isEmpty);
      expect(page.comboChain, [
        {'old': '2011', 'new': '2012', 'key': '2'},
      ]);
      expect(page.frameSaved.containsKey('2013'), false);
      expect(page.frameSaved['2011'], isEmpty);
      expect(page.counterSaved['2012'], isNull);
      expect(page.stageEffects.containsKey('2013'), false);
      expect(page.comboRuleBlackDraft, [
        {'prev': '101', 'cur': '102'},
      ]);
      expect(page.comboRuleInfo['rules']['black'], [
        {'prev': '101', 'cur': '102'},
      ]);
      await page.saveWorkspace();
      final applying = page.execute('weapon_apply');
      await tester.pumpAndSettle();
      await tester.tap(find.text('确认写入'));
      await tester.pumpAndSettle();
      await applying;
      expect(applied!['remaps'], saved!['remaps']);
      expect(applied!['cleared'], saved!['cleared']);
      expect(applied!['extra_properties'], saved!['extra_properties']);
      expect(
        calls.where(
          (op) => [
            'weapon_remap',
            'weapon_property_add',
            'weapon_state_clear',
          ].contains(op),
        ),
        isEmpty,
      );
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('module saves update memory workspace without legacy RPCs', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1600, 1200);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final calls = <String>[];
    Map<String, dynamic>? savedWorkspace;
    Map<String, dynamic>? appliedWorkspace;
    Future<dynamic> api(Map<String, dynamic> request) async {
      final operation = '${request['operation']}';
      calls.add(operation);
      if (operation == 'weapon_list') {
        return {
          'revision': 'memory-test',
          'weapons': [
            {
              'id': 253013,
              'name': '内存测试武器',
              'type': '测试',
              'combos': <dynamic>[],
              'stages': [
                {
                  'stage': 1,
                  'state': '1',
                  'label': 'C',
                  'action': '2001001',
                  'property_ids': <String>[],
                  'hits': <dynamic>[],
                  'supported': true,
                  'reason': '',
                },
              ],
            },
          ],
          'fields': <dynamic>[],
          'effects': <dynamic>[],
          'hit_options': <String, dynamic>{},
          'buffs': <dynamic>[],
          'applied': <String, dynamic>{},
          'drafts': <String, dynamic>{},
          'created': <String, dynamic>{},
          'states': [1, 2],
          'ustates': <dynamic>[],
          'undeployed': <dynamic>[],
        };
      }
      if (operation == 'client_directory_get') return <String, dynamic>{};
      if (operation == 'weapon_detail') {
        final weapon = {
          'id': 253013,
          'name': '内存测试武器',
          'type': '测试',
          'combos': <dynamic>[],
          'stages': [
            {
              'stage': 1,
              'state': '1',
              'label': 'C',
              'action': '2001001',
              'property_ids': <String>[],
              'hits': <dynamic>[],
              'supported': true,
              'reason': '',
            },
          ],
        };
        return {
          'revision': 'memory-test',
          'weapon': weapon,
          'weapons': [weapon],
          'fields': <dynamic>[],
          'effects': <dynamic>[],
          'hit_options': <String, dynamic>{},
          'buffs': <dynamic>[],
          'applied': <String, dynamic>{},
          'drafts': <String, dynamic>{},
          'created': <String, dynamic>{},
          'states': [1, 2],
          'ustates': <dynamic>[],
          'chain_info': {
            'chain': [
              {'old': '1', 'new': '2', 'key': '1'},
            ],
            'dead_ends': <dynamic>[],
            'frame_switches': [
              {'state': '1', 'attrs': <dynamic>[]},
            ],
            'frame_switches_saved': {
              '1': [
                {'attrs': <dynamic>[]},
              ],
            },
            'frame_keys': <dynamic>[],
            'counters': [
              {'state': '1', 'attrs': <dynamic>[], 'box': <dynamic>[]},
            ],
            'counters_saved': {
              '1': {'attrs': <dynamic>[], 'box': <dynamic>[]},
            },
            'block_elements': <dynamic>[],
            'block_elements_saved': <String, dynamic>{},
            'block_element_groups': <dynamic>[],
            'variants': <String, dynamic>{},
            'variants_saved': <String, dynamic>{},
            'variant_bases': <String, dynamic>{},
            'variant_occupied_ids': <dynamic>[],
            'variant_skillpro_min': 910000000,
            'variant_skillpro_max': 910000010,
            'keys': <dynamic>[],
          },
          'combo_rule_info': {
            'rules': {
              'max': <dynamic>[],
              'black': <dynamic>[],
              'white': <dynamic>[],
            },
            'editable': true,
          },
        };
      }
      if (operation == 'weapon_workspace_load') {
        return {'exists': true, 'payload': savedWorkspace};
      }
      if (operation == 'weapon_apply') {
        appliedWorkspace = Map<String, dynamic>.from(
          request['workspace'] as Map,
        );
        return {'message': 'applied'};
      }
      if (operation == 'weapon_workspace_save') {
        savedWorkspace = Map<String, dynamic>.from(request['workspace'] as Map);
        final workspace = Map<String, dynamic>.from(
          request['workspace'] as Map,
        );
        expect(workspace['combo_chain'], [
          {'old': '2', 'new': '1', 'key': '2'},
        ]);
        expect((workspace['frame_switches'] as List).single, {
          'state': '2',
          'attrs': [
            {'key': 'nextstate', 'value': '3'},
            {'key': 'keycode', 'value': '8'},
            {'key': 'window', 'value': '4-9'},
          ],
        });
        expect((workspace['counters'] as List).single, {
          'state': '2',
          'attrs': [
            {'key': 'startframe', 'value': '2'},
            {'key': 'endframe', 'value': '5'},
            {'key': 'nextstate', 'value': '7'},
          ],
          'box': [
            {'key': 'centerx', 'value': '1'},
            {'key': 'centery', 'value': '2'},
            {'key': 'centerz', 'value': '3'},
            {'key': 'length', 'value': '4'},
            {'key': 'width', 'value': '5'},
            {'key': 'heigth', 'value': '6'},
          ],
        });
        expect((workspace['combo_rule_info'] as Map)['rules'], {
          'max': [
            {
              'skill': '910000000',
              'max_combo': '3',
              'exceed_state': '',
              'exceed_skill_pro_id': '',
            },
          ],
          'black': [
            {'prev': '910000000', 'cur': '910000001'},
          ],
          'white': [
            {'prev': '910000001', 'cur': ''},
          ],
        });
        expect((workspace['scope_saved'] as Map)['1']['segment-1'], [
          {'key': 'centerx', 'value': '1'},
          {'key': 'centery', 'value': '2'},
          {'key': 'centerz', 'value': '3'},
          {'key': 'length', 'value': '4'},
          {'key': 'width', 'value': '5'},
          {'key': 'heigth', 'value': '6'},
        ]);
        return {'message': 'workspace saved', 'saved_at': 'test-now'};
      }
      fail('Unexpected operation: $operation');
    }

    await tester.pumpWidget(MaterialApp(home: WeaponConfigPage(api: api)));
    await tester.pumpAndSettle();
    await tester.tap(find.text('内存测试武器'));
    await tester.pumpAndSettle();
    final dynamic page = tester.state(find.byType(WeaponConfigPage));
    page.frameEdits = {
      '2': [
        {
          'attrs': [
            {'key': 'nextstate', 'value': '3'},
            {'key': 'keycode', 'value': '8'},
            {'key': 'window', 'value': '4-9'},
          ],
        },
      ],
    };
    page.counterEdits = {
      '2': {
        'attrs': [
          {'key': 'startframe', 'value': '2'},
          {'key': 'endframe', 'value': '5'},
          {'key': 'nextstate', 'value': '7'},
        ],
        'box': [
          {'key': 'centerx', 'value': '1'},
          {'key': 'centery', 'value': '2'},
          {'key': 'centerz', 'value': '3'},
          {'key': 'length', 'value': '4'},
          {'key': 'width', 'value': '5'},
          {'key': 'heigth', 'value': '6'},
        ],
      },
    };
    page.chainDraft = [
      {'old': '2', 'new': '1', 'key': '2'},
    ];
    page.comboRuleMaxDraft = [
      {
        'skill': '910000000',
        'max_combo': '3',
        'exceed_state': '',
        'exceed_skill_pro_id': '',
      },
    ];
    page.comboRuleBlackDraft = [
      {'prev': '910000000', 'cur': '910000001'},
    ];
    page.comboRuleWhiteDraft = [
      {'prev': '910000001', 'cur': ''},
    ];
    page.scopeSaved = {
      '1': {
        'segment-1': [
          {'key': 'centerx', 'value': '1'},
          {'key': 'centery', 'value': '2'},
          {'key': 'centerz', 'value': '3'},
          {'key': 'length', 'value': '4'},
          {'key': 'width', 'value': '5'},
          {'key': 'heigth', 'value': '6'},
        ],
      },
    };
    await page.saveFrameSwitches();
    await page.saveCounters();
    await page.saveChain();
    await page.saveComboRule();
    expect(calls.where((operation) => operation.endsWith('_set')), isEmpty);
    expect(page.dirty, isTrue);
    expect(page.frameEditing, isFalse);
    expect(page.counterEditing, isFalse);
    expect(page.chainEditing, isFalse);
    expect(page.comboRuleEditing, isFalse);

    page.startFrameEdit();
    page.frameEdits = {
      '1': [
        {'attrs': <dynamic>[]},
      ],
    };
    page.cancelFrameEdit();
    page.startCounterEdit();
    page.counterEdits = {'1': null};
    page.cancelCounterEdit();
    page.startChainEdit();
    page.chainDraft = [
      {'old': '9', 'new': '8', 'key': '1'},
    ];
    page.cancelChainEdit();
    page.startComboRuleEdit();
    page.comboRuleMaxDraft = <Map<String, dynamic>>[];
    page.cancelComboRuleEdit();
    expect(page.frameSaved.containsKey('1'), isTrue);
    expect(page.counterSaved.containsKey('1'), isTrue);
    expect(page.comboChain, [
      {'old': '2', 'new': '1', 'key': '2'},
    ]);
    expect(page.comboRuleMaxDraft, [
      {
        'skill': '910000000',
        'max_combo': '3',
        'exceed_state': '',
        'exceed_skill_pro_id': '',
      },
    ]);

    await page.saveWorkspace();
    await tester.pumpAndSettle();
    final submittedWorkspace = savedWorkspace;
    expect(
      calls.where((operation) => operation == 'weapon_workspace_save'),
      hasLength(1),
    );
    expect(calls.where((operation) => operation.endsWith('_set')), isEmpty);

    page.startComboRuleEdit();
    page.comboRuleMaxDraft = [
      {
        'skill': 'bad-draft',
        'max_combo': '99',
        'exceed_state': '99',
        'exceed_skill_pro_id': 'bad',
      },
    ];
    page.comboRuleBlackDraft = <Map<String, dynamic>>[];
    page.comboRuleWhiteDraft = <Map<String, dynamic>>[];
    page.cancelComboRuleEdit();
    expect(page.comboRuleInfo['rules'], {
      'max': [
        {
          'skill': '910000000',
          'max_combo': '3',
          'exceed_state': '',
          'exceed_skill_pro_id': '',
        },
      ],
      'black': [
        {'prev': '910000000', 'cur': '910000001'},
      ],
      'white': [
        {'prev': '910000001', 'cur': ''},
      ],
    });
    expect(page.comboRuleMaxDraft.single['skill'], '910000000');

    await page.saveComboRule(clear: true);
    await page.saveWorkspace();
    await tester.pumpAndSettle();
    expect((savedWorkspace!['combo_rule_info'] as Map)['rules'], {
      'max': <dynamic>[],
      'black': <dynamic>[],
      'white': <dynamic>[],
    });
    expect(savedWorkspace!['combo_rule_max_draft'], <dynamic>[]);
    expect(savedWorkspace!['combo_rule_black_draft'], <dynamic>[]);
    expect(savedWorkspace!['combo_rule_white_draft'], <dynamic>[]);

    savedWorkspace = submittedWorkspace;
    await page.loadWorkspace();
    await tester.pumpAndSettle();
    expect(page.comboRuleMaxDraft.single['skill'], '910000000');
    page.comboRuleMaxDraft[0]['max_combo'] = 'bad-draft';
    page.comboRuleInfo['rules']['max'][0]['max_combo'] = '4';
    final applyFuture = page.execute('weapon_apply');
    await tester.pumpAndSettle();
    await tester.tap(find.text('确认写入'));
    await applyFuture;
    await tester.pumpAndSettle();
    expect((appliedWorkspace!['combo_rule_info'] as Map)['rules']['max'], [
      {
        'skill': '910000000',
        'max_combo': '4',
        'exceed_state': '',
        'exceed_skill_pro_id': '',
      },
    ]);
    expect(calls.where((operation) => operation.endsWith('_set')), isEmpty);
    expect(tester.takeException(), isNull);
  });

  testWidgets('C combo example saves independent effects without applying', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1440, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final calls = <String>[];
    List<String> logicalCalls() =>
        calls.where((operation) => operation != 'shop_images').toList();
    List<dynamic> draft = [];
    Future<dynamic> api(Map<String, dynamic> request) async {
      calls.add(request['operation']);
      if (request['operation'] == 'weapon_list' ||
          request['operation'] == 'weapon_detail') {
        final catalog = {
          'revision': 'test-revision',
          'fields': [
            {'key': 'SkillDamage', 'name': '基础伤害', 'min': 0, 'max': 10000},
          ],
          'applied': <String, dynamic>{},
          'drafts': {'253013': draft},
          'buffs': [
            {'id': 0, 'name': '保持原效果'},
            {'id': 1, 'name': '中毒'},
            {'id': 37, 'name': '燃烧（献祭燃烧）'},
          ],
          'weapons': [
            {
              'id': 253013,
              'name': '骤足秘笈',
              'combos': [
                {
                  'name': '站立攻击1',
                  'nodes': [
                    {'keys': 'C', 'state': '1'},
                    {'keys': 'CC', 'state': '2'},
                  ],
                },
              ],
              'stages': List.generate(
                5,
                (i) => {
                  'stage': i + 1,
                  'state': '${i + 1}',
                  'label': i < 2 ? ['C', 'CC'][i] : '跑动普通攻击（动画说明，非按键）',
                  'action': '${2001130 + i}',
                  'property_ids': ['8081$i'],
                  'hits': [
                    {
                      'id': '8081$i',
                      'values': {'SkillDamage': '3'},
                      'buff': '0',
                    },
                  ],
                  'supported': true,
                  'reason': '',
                },
              ),
            },
          ],
        };
        if (request['operation'] == 'weapon_list') {
          return catalog;
        }
        final detailWeapon = Map<String, dynamic>.from(
          (catalog['weapons'] as List).single as Map,
        );
        return {
          ...catalog,
          'weapon': detailWeapon,
          'weapons': [detailWeapon],
          'chain_info': {
            'chain': <dynamic>[],
            'dead_ends': <dynamic>[],
            'frame_switches': <dynamic>[],
            'frame_switches_saved': <String, dynamic>{},
            'frame_keys': <dynamic>[],
            'counters': <dynamic>[],
            'counters_saved': <String, dynamic>{},
            'block_elements': <dynamic>[],
            'block_elements_saved': <String, dynamic>{},
            'block_element_groups': <dynamic>[],
            'variants': <String, dynamic>{},
            'variants_saved': <String, dynamic>{},
            'variant_bases': <String, dynamic>{},
            'variant_occupied_ids': <dynamic>[],
            'variant_skillpro_min': 910000000,
            'variant_skillpro_max': 910000010,
            'keys': <dynamic>[],
          },
          'combo_rule_info': {'rules': {}, 'editable': true},
        };
      }
      expect(request['operation'], 'weapon_workspace_save');
      expect(request['weapon'], 253013);
      final workspace = Map<String, dynamic>.from(request['workspace'] as Map);
      draft = (workspace['rules'] as List)
          .map((r) => Map<String, dynamic>.from(r as Map))
          .toList();
      return {'message': '当前武器暂存已保存', 'saved_at': 'test'};
    }

    await tester.pumpWidget(MaterialApp(home: WeaponConfigPage(api: api)));
    await tester.pumpAndSettle();
    expect(logicalCalls(), ['weapon_list', 'client_directory_get']);
    expect(find.text('请从左侧选择武器查看配置'), findsOneWidget);
    await tester.tap(find.text('骤足秘笈'));
    await tester.pumpAndSettle();
    expect(logicalCalls(), [
      'weapon_list',
      'client_directory_get',
      'weapon_detail',
    ]);
    expect(find.text('CC'), findsOneWidget);
    await tester.tap(find.text('CC'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('第 1 段 · C'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('填入示例：第一下中毒，第二下燃烧'));
    await tester.pumpAndSettle();
    expect(find.text('DEBUFF'), findsWidgets);
    expect(find.text('受击动作'), findsWidgets);
    expect(find.text('默认（原受击动作）'), findsWidgets);
    expect(find.text('击飞参数 / 高级设置'), findsWidgets);
    expect(
      find.byWidgetPredicate(
        (w) => w is SelectableText && (w.data ?? '').contains('原配置：'),
      ),
      findsNothing,
    );
    expect(find.widgetWithText(TextFormField, '基础伤害'), findsWidgets);
    await tester.enterText(
      find.widgetWithText(TextFormField, '基础伤害').first,
      '7',
    );
    await tester.pumpAndSettle();
    final durations = find.widgetWithText(TextFormField, '持续周期（原生值）');
    await tester.enterText(durations.first, '4500');
    await tester.pumpAndSettle();
    await tester.tap(find.text('保存方案'));
    await tester.pumpAndSettle();
    expect(draft[0]['buff'], 1);
    expect(draft[0]['properties']['80810']['SkillDamage'], 7);
    expect(draft[0]['duration'], 4500);
    expect(draft[1]['buff'], 37);
    expect(draft[2]['buff'], 0);
    expect(calls, isNot(contains('weapon_apply')));
    expect(find.text('当前武器暂存已保存'), findsOneWidget);
    expect(logicalCalls(), [
      'weapon_list',
      'client_directory_get',
      'weapon_detail',
      'weapon_workspace_save',
    ]);
    expect(
      logicalCalls().where((operation) => operation == 'weapon_list'),
      hasLength(1),
    );
    expect(find.text('更新到线上'), findsNothing);
    expect(find.text('发布到线上'), findsNothing);
    expect(find.text('导出发版包'), findsOneWidget);
    expect(calls, isNot(contains('weapon_publish')));
    expect(tester.takeException(), isNull);
  });
}
