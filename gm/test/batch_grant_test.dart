import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/batch_grant.dart';

void main(){
 testWidgets('history resumes only unsuccessful users with the original batch id',(tester)async{
  tester.view.physicalSize=const Size(1200,900);tester.view.devicePixelRatio=1;
  addTearDown(tester.view.resetPhysicalSize);addTearDown(tester.view.resetDevicePixelRatio);
  final sent=<int>[];
  final rows=[{'uid':1,'account':'done','state':'success','detail':'','updated':'today'},{'uid':2,'account':'retry','state':'failed','detail':'capacity','updated':'today'}];
  dynamic status()=>{'id':'original','items':[{'name':'card','quantity':88}],'recipients':rows,'total':2,'success':rows.where((r)=>r['state']=='success').length,'failed':rows.where((r)=>r['state']=='failed').length,'delivered_quantity':88*rows.where((r)=>r['state']=='success').length};
  await tester.pumpWidget(MaterialApp(home:BatchGrantPage(environment:'测试',api:(r)async{
   switch(r['operation']){
    case 'catalog':return {'items':[]};
    case 'accounts':return [];
    case 'grant_batch_list':return [{'id':'original','created':'today','success':1,'failed':1,'total':2}];
    case 'grant_batch_get':expect(r['id'],'original');return status();
    case 'grant_batch_send_many':expect(r['id'],'original');sent.addAll(List<int>.from(r['uids']));rows[1]['state']='success';rows[1]['detail']='';return status();
   }
   throw StateError('unexpected operation');
  })));
  await tester.pumpAndSettle();await tester.tap(find.text('历史批次 / 恢复进度'));await tester.pumpAndSettle();
  await tester.tap(find.textContaining('today · original'));await tester.pumpAndSettle();
  await tester.tap(find.text('开始 / 继续未成功用户'));await tester.pumpAndSettle();
  expect(sent,[2]);expect(find.textContaining('成功 2 · 失败 0'),findsOneWidget);
 });
 testWidgets('ticket-only batch records amount and audience', (tester) async {
  tester.view.physicalSize=const Size(1400,1000);tester.view.devicePixelRatio=1;
  addTearDown(tester.view.resetPhysicalSize);addTearDown(tester.view.resetDevicePixelRatio);
  Map<String,dynamic>? request;
  await tester.pumpWidget(MaterialApp(home:BatchGrantPage(environment:'测试',api:(r) async {
   if(r['operation']=='grant_batch_list')return [];
   if(r['operation']=='catalog')return {'items':[]};
   if(r['operation']=='accounts')return [];
   if(r['operation']=='grant_batch_create') {request=r; throw Exception('retain original batch for retry');}
   throw StateError('unexpected');
  })));
  await tester.pumpAndSettle();
  await tester.tap(find.text('添加点券'));await tester.pumpAndSettle();
  await tester.enterText(find.byKey(const ValueKey('currency:ticket-quantity')), '50000');
  await tester.tap(find.byType(Switch));await tester.pumpAndSettle();
  await tester.tap(find.text('保存发放批次'));await tester.pumpAndSettle();
  expect(find.text('点券 × 50000'),findsOneWidget);
  await tester.tap(find.text('保存批次'));await tester.pumpAndSettle();
  expect(request?['all'],true);
  expect((request?['batch_items'] as List).single['quantity'],50000);
  expect((request?['batch_items'] as List).single['key'],'currency:ticket');
 });
}
