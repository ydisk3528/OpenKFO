import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/weapon_config.dart';

void main() {
  testWidgets(
    'weapon icons, descriptions and type filters keep combo selection',
    (tester) async {
      tester.view.physicalSize = const Size(1280, 800);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      const imageData =
          'iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAEUlEQVR4nGP4z8DwH4QZYAwAR8oH+WdZbrcAAAAASUVORK5CYII=';
      final calls = <String>[];
      Map<String, dynamic> weapon(
        int id,
        String name,
        String type,
        String path,
      ) => {
        'id': id,
        'name': name,
        'type': type,
        'icon': path,
        'description': '$name 的原始简介',
        'stages': <dynamic>[],
        'combos': [
          {
            'name': '$name 连招',
            'nodes': [
              {'state': '1', 'keys': 'CCX'},
            ],
          },
        ],
      };
      final weapons = [
        weapon(1, '烈焰刀', '刀类', 'unused-local-path.png'),
        weapon(2, '寒冰剑', '剑类', ''),
      ];
      final common = {
        'revision': 'test',
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
        if (request['operation'] == 'shop_images') return {'25:1': imageData};
        if (request['operation'] == 'weapon_list') {
          return {...common, 'weapons': weapons};
        }
        if (request['operation'] == 'client_directory_get') {
          return <String, dynamic>{};
        }
        if (request['operation'] == 'weapon_detail') {
          final selected = weapons.singleWhere(
            (value) => value['id'] == request['weapon'],
          );
          return {
            ...common,
            'weapon': selected,
            'weapons': [selected],
            'chain_info': <String, dynamic>{},
            'combo_rule_info': <String, dynamic>{},
          };
        }
        fail('Unexpected operation: ${request['operation']}');
      }

      await tester.pumpWidget(MaterialApp(home: WeaponConfigPage(api: api)));
      await tester.runAsync(
        () => precacheImage(
          MemoryImage(base64Decode(imageData)),
          tester.element(find.byType(WeaponConfigPage)),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('请从左侧选择武器查看配置'), findsOneWidget);
      await tester.tap(find.widgetWithText(ListTile, '烈焰刀'));
      await tester.pumpAndSettle();
      expect(find.text('烈焰刀 的原始简介'), findsOneWidget);
      expect(find.byType(Image), findsNWidgets(2));
      expect(
        tester.getTopLeft(find.text('武器简介')).dy,
        lessThan(tester.getTopLeft(find.text('连招与命中效果')).dy),
      );
      await tester.tap(find.byType(DropdownButtonFormField<String>));
      await tester.pumpAndSettle();
      await tester.tap(find.text('剑类').last);
      await tester.pumpAndSettle();
      expect(find.text('共 2 件武器'), findsOneWidget);
      expect(find.widgetWithText(ListTile, '寒冰剑'), findsOneWidget);
      expect(find.widgetWithText(ListTile, '烈焰刀'), findsNothing);
      await tester.tap(find.widgetWithText(ListTile, '寒冰剑'));
      await tester.pumpAndSettle();
      expect(find.text('寒冰剑 的原始简介'), findsOneWidget);
      expect(find.text('CCX'), findsOneWidget);
      expect(find.byIcon(Icons.image_not_supported_outlined), findsNWidgets(2));
      await tester.enterText(find.byType(TextField), '不存在');
      await tester.pumpAndSettle();
      expect(find.widgetWithText(ListTile, '寒冰剑'), findsNothing);
      expect(find.widgetWithText(ListTile, '烈焰刀'), findsNothing);
      expect(calls.where((c) => c == 'weapon_list'), hasLength(1));
      expect(calls.where((c) => c == 'weapon_detail'), hasLength(2));
      expect(calls, contains('shop_images'));
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
    },
  );
}
