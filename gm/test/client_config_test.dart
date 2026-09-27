import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/client_config.dart';
void main() {
 testWidgets('packaging requires preview and invalidates it after version edit', (tester) async {
  tester.view.physicalSize=const Size(1500,1800);tester.view.devicePixelRatio=1;
  addTearDown(tester.view.resetPhysicalSize);addTearDown(tester.view.resetDevicePixelRatio);
  final calls=<String>[];
  Future<dynamic> api(Map<String,dynamic> req) async {
   calls.add(req['operation']);
   if(req['operation']=='client_config_plans') return {'plans':[{'id':'maps-1','category':'maps','name':'双梯方案'}],'folder':'plans','base':'base.spf2','resource_root':'client'};
   if(req['operation']=='client_config_preview') {expect(req['client_config']['selected'],['maps-1']);return {'preview':'checked','changes':['地图805'],'files':{'Data/config.spf2':'hash'},'message':'检查通过'};}
   throw StateError('unexpected request');
  }
  await tester.pumpWidget(MaterialApp(home:ClientConfigPage(api:api)));await tester.pumpAndSettle();
  await tester.tap(find.text('打包发布'));await tester.pumpAndSettle();
  expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton,'生成 OSS 更新包')).onPressed,isNull);
  await tester.tap(find.text('双梯方案'));await tester.tap(find.text('预览合并并检查'));await tester.pumpAndSettle();
  expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton,'生成 OSS 更新包')).onPressed,isNotNull);
  await tester.enterText(find.widgetWithText(TextField,'新版本号'),'changed-version');await tester.pump();
  expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton,'生成 OSS 更新包')).onPressed,isNull);
  expect(calls.contains('client_config_build'),isFalse);
 });
}
