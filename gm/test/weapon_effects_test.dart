import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/weapon_config.dart';

void main() {
  testWidgets('effect repair previews then writes selected weapon and revision', (tester) async {
    tester.view.physicalSize = const Size(1600, 1000);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    var writes = 0;
    Future<dynamic> api(Map<String, dynamic> req) async {
      switch (req['operation']) {
        case 'weapon_effects_preview':
          expect(req['weapon'], 253300);
          return {'revision': 'live-hash', 'path': 'client/Data/config.spf2',
            'additions': [{'id': '183011', 'file': '183011'}],
            'issues': ['特效 6001178 没有原始登记']};
        case 'weapon_effects_apply':
          expect(req['weapon'], 253300);
          expect(req['revision'], 'live-hash');
          writes++;
          return {'message': '已补齐 1 条攻击特效'};
        default:
          return {'revision': 'catalog', 'drafts': <String,dynamic>{},
            'applied': <String,dynamic>{}, 'weapons': [
              {'id': 253300, 'name': '流氓拳', 'stages': [], 'combos': []}
            ]};
      }
    }
    await tester.pumpWidget(MaterialApp(home: WeaponConfigPage(api: api)));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('自动补齐攻击特效'));
    await tester.tap(find.text('自动补齐攻击特效'));
    await tester.pumpAndSettle();
    expect(writes, 0);
    expect(find.textContaining('6001178'), findsOneWidget);
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    expect(writes, 0);
    await tester.tap(find.text('自动补齐攻击特效'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('备份并补齐'));
    await tester.pumpAndSettle();
    expect(writes, 1);
    expect(find.text('已补齐 1 条攻击特效'), findsOneWidget);
  });
}
