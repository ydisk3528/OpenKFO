import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/reward_config.dart';
import 'package:kungfu_item_manager/reward_table.dart';

void main() {
  testWidgets(
    'copy online previews first, cancels safely and uses target revision',
    (tester) async {
      tester.view.physicalSize = const Size(1400, 1000);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final writes = <Map<String, dynamic>>[];
      Future<dynamic> api(Map<String, dynamic> r) async {
        if (r['operation'] == 'rewards_save') writes.add(r);
        return {
          'revision': r['environment'] == 'online' ? 9 : 3,
          'rules': {
            'levels': rewardRows({
              'win_gold': r['environment'] == 'online' ? 0 : 20,
            }),
          },
        };
      }

      await tester.pumpWidget(
        MaterialApp(
          home: RewardConfigPage(
            api: api,
            environment: '本地测试服',
            environmentApi: api,
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.text('本地复制到线上（预览差异）'));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 400));
      expect(writes, isEmpty);
      expect(find.text('确认用本地已保存配置覆盖线上？'), findsOneWidget);
      await tester.tap(find.text('取消'));
      await tester.pumpAndSettle();
      expect(writes, isEmpty);
      await tester.tap(find.text('本地复制到线上（预览差异）'));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 400));
      await tester.tap(find.text('确认'));
      await tester.pumpAndSettle();
      expect(writes.single['environment'], 'online');
      expect(writes.single['reward_revision'], 9);
      expect(writes.single['rewards']['levels'].length, 200);
      expect(tester.takeException(), isNull);
    },
  );
  testWidgets('bulk changes remain draft until save', (tester) async {
    tester.view.physicalSize = const Size(1400, 1000);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    Map<String, dynamic>? saved;
    Future<dynamic> api(Map<String, dynamic> r) async {
      if (r['operation'] == 'rewards_save') saved = r;
      return {
        'revision': 4,
        'rules':
            saved?['rewards'] ??
            {
              'win_gold': 20,
              'stage_rewards': [
                {
                  'map_id': 20051,
                  'clear': {'experience': 12},
                  'failed': {'gold': 1},
                },
              ],
              'level_gifts': [
                {
                  'level': 5,
                  'items': [456],
                },
              ],
              'drops': [
                {
                  'catalog_key': 123,
                  'outcome': 'win',
                  'min_level': 1,
                  'max_level': 150,
                  'chance_per_10000': 100,
                },
              ],
            },
      };
    }

    await tester.pumpWidget(
      MaterialApp(
        home: RewardConfigPage(api: api, environment: '本地测试服'),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('批量修改'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const ValueKey('win_gold')), '0');
    await tester.tap(find.text('应用到表格'));
    await tester.pumpAndSettle();
    expect(saved, isNull);
    await tester.tap(find.text('保存奖励配置'));
    await tester.pumpAndSettle();
    expect(saved?['reward_revision'], 4);
    expect(saved?['rewards']['levels'][149]['win_gold'], 0);
    expect(saved?['rewards']['drops'][0]['catalog_key'], 123);
    expect(saved?['rewards']['level_gifts'][0]['items'], [456]);
    expect(saved?['rewards']['stage_rewards'][0]['clear']['experience'], 12);
    expect(tester.takeException(), isNull);
  });
}
