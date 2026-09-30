import 'package:file_selector_platform_interface/file_selector_platform_interface.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/weapon_merge_page.dart';

class Picker extends FileSelectorPlatform {
  int opens = 0;
  @override
  Future<XFile?> openFile({
    List<XTypeGroup>? acceptedTypeGroups,
    String? initialDirectory,
    String? confirmButtonText,
  }) async {
    opens++;
    expect(acceptedTypeGroups!.single.extensions, ['zip']);
    return XFile('X:/weapon.zip');
  }

  @override
  Future<FileSaveLocation?> getSaveLocation({
    List<XTypeGroup>? acceptedTypeGroups,
    SaveDialogOptions options = const SaveDialogOptions(),
  }) async => const FileSaveLocation('X:/saved.zip');
}

void main() {
  testWidgets(
    'file picker compares selection and stages before explicit application',
    (tester) async {
      tester.view.physicalSize = const Size(1440, 1000);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final old = FileSelectorPlatform.instance, picker = Picker();
      FileSelectorPlatform.instance = picker;
      addTearDown(() => FileSelectorPlatform.instance = old);
      final calls = <String>[];
      Future<dynamic> api(Map<String, dynamic> r) async {
        calls.add(r['operation']);
        switch (r['operation']) {
          case 'weapon_merge_packages':
            return {'packages': []};
          case 'weapon_merge_compare':
            expect(r['source_path'], 'X:/weapon.zip');
            return {
              'revision': 'proof',
              'reference': 'game/Data/config.spf2',
              'new': [
                {'id': 2, 'name': '新武器'},
              ],
              'modified': [
                {
                  'id': 1,
                  'name': '旧武器',
                  'changes': ['攻击特效绑定'],
                },
              ],
              'unchanged': [
                {'id': 3, 'name': '相同武器'},
              ],
            };
          case 'weapon_merge_stage':
            expect(r['revision'], 'proof');
            expect(r['merge_weapons'], [2]);
            return {
              'workspace': 'merge-1',
              'path': 'temp/Data/config.spf2',
              'new': [
                {'id': 2},
              ],
              'modified': [],
              'assets': 2,
              'entries': [],
              'message': '临时配置完成',
            };
          case 'weapon_merge_save':
            expect(r['merge_workspace'], 'merge-1');
            return {'path': 'X:/saved.zip', 'message': '保存完成'};
          default:
            throw StateError('unexpected ${r['operation']}');
        }
      }

      await tester.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (c) => Scaffold(
              body: TextButton(
                onPressed: () => Navigator.push(
                  c,
                  MaterialPageRoute(builder: (_) => WeaponMergePage(api: api)),
                ),
                child: const Text('打开合并'),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('打开合并'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('选择武器 ZIP'));
      await tester.pumpAndSettle();
      expect(picker.opens, 1);
      expect(find.text('初始化完成 · 选择差异合并'), findsOneWidget);
      expect(calls.contains('weapon_merge_stage'), isFalse);
      await tester.tap(find.text('旧武器 · 1'));
      await tester.pump();
      await tester.tap(find.text('合并 1 把到临时配置'));
      await tester.pumpAndSettle();
      expect(calls.contains('weapon_merge_stage'), isTrue);
      expect(calls.contains('weapon_merge_apply'), isFalse);
      await tester.tap(find.byIcon(Icons.arrow_back));
      await tester.pumpAndSettle();
      expect(find.text('临时配置尚未保存'), findsOneWidget);
      await tester.tap(find.text('保存后返回'));
      await tester.pumpAndSettle();
      expect(calls.contains('weapon_merge_save'), isTrue);
      expect(find.text('打开合并'), findsOneWidget);
      expect(tester.takeException(), isNull);
    },
  );
}
