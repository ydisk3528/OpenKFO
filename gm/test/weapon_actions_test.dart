import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/weapon_config.dart';
void main(){
 testWidgets('missing combo tips do not hide a special move or shift edited rule',(tester)async{
  tester.view.physicalSize=const Size(1440,1000);tester.view.devicePixelRatio=1;
  addTearDown(tester.view.resetPhysicalSize);addTearDown(tester.view.resetDevicePixelRatio);
  List<dynamic> saved=[];
  Future<dynamic> api(Map<String,dynamic> r)async{
   if(r['operation']=='weapon_save'){saved=r['rules'];return {'message':'已保存'};}
   return {'revision':'test','drafts':<String,dynamic>{},'applied':<String,dynamic>{},'buffs':[{'id':0,'name':'保持原效果'}],'fields':[{'key':'SkillDamage','name':'基础伤害','min':0,'max':10000}],
    'weapons':[{'id':253030,'name':'凌云神腕','combos':[], 'stages':[
     {'stage':1011,'state':'1011','label':'移动','action':'1001013','supported':false,'reason':'无命中','property_ids':[],'hits':[]},
     {'stage':2021,'state':'2021','label':'招式 2021','action':'2001365','supported':true,'reason':'','property_ids':['811106'],'hits':[{'id':'811106','values':{'SkillDamage':'9'}}]},
    ]}]};
  }
  await tester.pumpWidget(MaterialApp(home:WeaponConfigPage(api:api)));await tester.pumpAndSettle();
  expect(find.text('动作说明（2）'),findsOneWidget);
  await tester.tap(find.text('招式 2021'));await tester.pumpAndSettle();
  expect(find.text('招式 2021'),findsWidgets);
  await tester.enterText(find.widgetWithText(TextFormField,'基础伤害'),'71');
  await tester.tap(find.text('保存方案'));await tester.pumpAndSettle();
  expect(saved[0]['stage'],1011);expect(saved[0]['properties'],isNull);
  expect(saved[1]['stage'],2021);expect(saved[1]['properties']['811106']['SkillDamage'],71);
  expect(tester.takeException(),isNull);
 });
}
