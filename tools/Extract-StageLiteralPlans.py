import re,json
from pathlib import Path
pat=re.compile(r'\s+|--[^\r\n]*|"(?:\\.|[^"\\])*"|\d+(?:\.\d+)?|[A-Za-z_]\w*|.',re.S)
class Reader:
 def __init__(self,s):self.t=[x for x in pat.findall(s) if not x.isspace() and not x.startswith('--')];self.i=0
 def pop(self):x=self.t[self.i];self.i+=1;return x
 def need(self,x):assert self.pop()==x,(x,self.t[self.i-3:self.i+3])
 def val(self):
  x=self.pop()
  if x=='{':
   d={};n=1
   while self.t[self.i]!='}':
    if self.t[self.i]=='[':self.pop();k=self.val();self.need(']');self.need('=');d[k]=self.val()
    elif self.t[self.i+1]=='=':k=self.pop();self.pop();d[k]=self.val()
    else:d[n]=self.val();n+=1
    if self.t[self.i] in (',',';'):self.pop()
    else:assert self.t[self.i]=='}'
   self.pop();return d
  if x=='EVENT_GROUP':self.need('(');v=self.val();self.need(')');return v
  if x=='-':return -self.val()
  if x.startswith('"'):return json.loads(x)
  if x in ('true','false'):return x=='true'
  if x[0].isdigit():return float(x) if '.' in x else int(x)
  raise ValueError('unknown '+x)
 def table(self,name):
  for i in range(len(self.t)-2):
   if self.t[i:i+2]==[name,'=']:self.i=i+2;return self.val()
  raise ValueError('missing '+name)

def build(root, catalogue):
 import hashlib
 rows=catalogue['pve_maps']; names=rows[0]['foster_templates']['names']
 assert hashlib.sha256((root/'script/pve/config.lua').read_bytes()).hexdigest()==rows[0]['foster_templates']['config_hash']
 plans={}
 for m in rows:
  if m['map_type']!=10:continue
  raw=(root/m['script']).read_bytes();r=Reader(raw.decode('gb18030'))
  events=r.table('event_boxs');players=r.table('players')
  match=re.search(r'map_init\(players,\s*event_boxs,\s*(\d+)\)',raw.decode('gb18030'));assert match
  p={'global_limit':int(match[1]),'player_limit':len(players),'groups':[]}
  for gi,e in events.items():
   assert set(e)=={'boxs','event'}
   g=e['event'];assert set(g)<={'subs','max','born','block'}
   subs=list(g['subs'].values())
   for si,sub in enumerate(subs):
    assert set(sub)<={'max','monsters','end_last_time'}
    x={'spawns':[],'sub_limit':sub['max'],'group_limit':g['max'],'trigger_box':list(e['boxs'][1].values())}
    if len(e['boxs'])>1:x['trigger_boxes']=[list(b.values()) for b in e['boxs'].values()]
    if si==0 and g.get('block'):x['block']=g['block']
    if len(subs)>1:x['family']=gi
    if si>0:x['previous_batch']=len(p['groups'])
    if 'end_last_time' in sub:x['end_after']=sub['end_last_time']
    for monster in sub['monsters'].values():
     if isinstance(monster,str):monster={'n':monster}
     assert set(monster)<={'n','p','d'}
     v={'template':names.index(monster['n']),'direction':monster.get('d',2),'position':[0,0,0]}
     if 'p' in monster:v['position']=list(monster['p'].values())
     else:v['born_box']=list(g['born'].values())
     x['spawns'].append(v)
    p['groups'].append(x)
  plans[hashlib.sha256(raw).hexdigest()]=p
 assert len(plans)==49
 return plans
if __name__=='__main__':
 import argparse
 cli=argparse.ArgumentParser(description='Parse original literal map tables without executing Lua.')
 cli.add_argument('--extracted-root',type=Path,required=True);cli.add_argument('--catalogue',type=Path,required=True);cli.add_argument('--output',type=Path,required=True)
 a=cli.parse_args();d=json.loads(a.catalogue.read_text(encoding='utf-8-sig'));d=d.get('result',d)
 a.output.write_text(json.dumps(build(a.extracted_root,d),ensure_ascii=False,separators=(',',':')),encoding='utf-8')
