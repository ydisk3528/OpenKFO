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
   if(req['operation']=='client_config_catalog') return {'files':[],'revision':'test','plans':[{'id':'maps-1','category':'maps','name':'双梯方案'}],'folder':'plans'};
   if(req['operation']=='client_config_preview') {expect(req['client_config']['selected'],['maps-1']);return {'preview':'checked','changes':['地图805'],'files':{'Data/config.spf2':'hash'},'message':'检查通过'};}
   throw StateError('unexpected request');
  }
  await tester.pumpWidget(MaterialApp(home:ClientConfigPage(api:api)));await tester.pumpAndSettle();
  expect(tester.takeException(),isNull);
  expect(find.text('状态/Buff'),findsOneWidget);
  expect(find.text('状态效果、属性及 Buff 定制'),findsOneWidget);
  await tester.tap(find.text('打包发布'));await tester.pumpAndSettle();
  expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton,'生成 OSS 更新包')).onPressed,isNull);
  await tester.tap(find.text('双梯方案'));await tester.tap(find.text('预览合并并检查'));await tester.pumpAndSettle();
  expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton,'生成 OSS 更新包')).onPressed,isNotNull);
  await tester.enterText(find.widgetWithText(TextField,'新版本号'),'changed-version');await tester.pump();
  expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton,'生成 OSS 更新包')).onPressed,isNull);
  expect(calls.contains('client_config_build'),isFalse);
 });
 testWidgets('client record changes ask to save before returning', (tester) async {
  tester.view.physicalSize=const Size(1440,1000);tester.view.devicePixelRatio=1;
  addTearDown(tester.view.resetPhysicalSize);addTearDown(tester.view.resetDevicePixelRatio);
  final calls=<String>[];
  Future<dynamic> api(Map<String,dynamic> r) async {
   calls.add(r['operation']);
   switch(r['operation']) {
    case 'client_config_plans':return {'plans':[],'folder':'plans','base':'base.spf2','resource_root':'client'};
    case 'client_config_catalog':return {'files':['item.txt'],'revision':'r1','plans':[],'folder':'plans'};
    case 'client_config_records':return {'revision':'r1','records':[{'key':'25:1','label':'测试武器','content':'before','values':{'名称':'原名'}}]};
    case 'client_config_save':expect(r['client_config']['values']['名称'],'新名');return {'message':'已保存'};
    case 'shop_images':return {};
   }
   throw StateError('unexpected request ${r['operation']}');
  }
  await tester.pumpWidget(MaterialApp(home:Builder(builder:(c)=>Scaffold(body:TextButton(onPressed:()=>Navigator.push(c,MaterialPageRoute(builder:(_)=>ClientConfigPage(api:api))),child:const Text('进入配置'))))));
  await tester.tap(find.text('进入配置'));await tester.pumpAndSettle();
  await tester.tap(find.text('读取配置'));await tester.pumpAndSettle();

  await tester.tap(find.text('测试武器'));await tester.pumpAndSettle();
  await tester.enterText(find.widgetWithText(TextField,'名称'),'新名');await tester.pump();
  await tester.tap(find.byIcon(Icons.arrow_back));await tester.pumpAndSettle();
  expect(find.text('有未保存的配置修改'),findsOneWidget);
  await tester.tap(find.text('保存后继续'));await tester.pumpAndSettle();
  expect(calls.contains('client_config_save'),isTrue);expect(find.text('进入配置'),findsOneWidget);
  expect(tester.takeException(),isNull);
 });

}
