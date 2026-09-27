"""Build an isolated chapter-three candidate; never overwrite an installed client."""
from pathlib import Path
import sys, struct, zlib, hashlib, json, re
sys.path.insert(0,str(Path(__file__).resolve().parents[2]/'server'))
from kk_local.spf2 import parse_paths, decode_config, CONFIG_KEY

def unpack(b):
    tr,tb,ck,n=struct.unpack_from('<4I',b,40)
    out={}
    for i,name in parse_paths(b[tr:tb],n).items():
        o,s=struct.unpack_from('<2I',b,tb+i*8)
        out[name]=decode_config(b[o+4:o+4+s])
    return out

def pack(header, entries):
    names=list(entries);body=bytearray(header[:64]);records=[];crcs=[]
    for name in names:
        raw=zlib.compress(entries[name]);raw=bytes(x^255^CONFIG_KEY[i%len(CONFIG_KEY)] for i,x in enumerate(raw))
        chunk=b'\x00\x22\x00\x00'+raw;records.append((len(body),len(raw)));crcs.append(zlib.crc32(chunk));body.extend(chunk)
    tree=bytearray()
    def node(rows):
        groups={}
        for text,index in rows:groups.setdefault(text[:8],[]).append((text,index))
        start=len(tree);tree.extend(struct.pack('<I',len(groups))+bytes(12*len(groups)))
        for i,key in enumerate(sorted(groups)):
            group=groups[key]
            prefix=group[0][0][:8]
            rest=[(text[len(prefix):],idx) for text,idx in group]
            target=(0xff000000|rest[0][1]) if len(rest)==1 and not rest[0][0] else node(rest)
            struct.pack_into('<8sI',tree,start+4+12*i,prefix,target)
        return start
    node([(n.encode('gb18030'),i) for i,n in enumerate(names)])
    tr=len(body);body.extend(tree);tb=len(body)
    for r in records:body.extend(struct.pack('<2I',*r))
    ck=len(body)
    for c in crcs:body.extend(struct.pack('<2I',c,0))
    struct.pack_into('<5I',body,40,tr,tb,ck,len(names),zlib.crc32(b''.join(struct.pack('<I',c) for c in crcs)))
    assert unpack(body)==entries
    # Native lookup consumes eight bytes at each node and uses sorted labels.
    for name in names:
        key=name.encode('gb18030');pos=0
        while True:
            count=struct.unpack_from('<I',tree,pos)[0]
            rows=[struct.unpack_from('<8sI',tree,pos+4+j*12) for j in range(count)]
            assert [x[0] for x in rows]==sorted(x[0] for x in rows)
            target=next(v for label,v in rows if label==key[:8].ljust(8,b'\0'))
            key=key[8:]
            if target>>24==255:
                assert not key and names[target&0xffffff]==name
                break
            pos=target
    return body

def build(source,out):
    out.mkdir(parents=True,exist_ok=False)
    original=source.read_bytes();data=unpack(original);before=dict(data)
    def get(n):return data['/'+n].decode('gb18030')
    def put(n,t):data['/'+n]=t.encode('gb18030')
    assert 'MapId="8170"' not in get('mapmgr.xml')
    mgr=get('mapmgr.xml');row=re.search(r'<MapConfig\b[^>]*MapId="803"[^>]*/>',mgr).group()
    new=row.replace('MapId="803"','MapId="8170"').replace('Priority="303"','Priority="2301"').replace('xmlfile="water3"','xmlfile="ice_chapter"').replace('MaxPlayer="8"','MaxPlayer="6"').replace('Name="冰封楼阁"','Name="冰封阁楼 · 雪阁守卫"')
    put('mapmgr.xml',mgr.replace(row,row+'\r\n'+new,1))
    ui=get('pveuiconfig.xml');put('pveuiconfig.xml',ui.replace('</PVEEntryUI>','<Map LogicType="1" Group="7" SelectDifficulty="1" MapID="8170" MapType="10" DisplayDifficulty="4" RewardItem1="" RewardItem2="" RewardItem3=""/>\r\n</PVEEntryUI>'))
    # Use the original exterior and collisions. No unrelated map is changed.
    scene=get('maps/water3.xml');scene=re.sub(r'<Map id="1"[\s\S]*?</Map>','',scene);scene=scene.replace('num="2"','num="1"');scene=re.sub(r'<JumpPoints>[\s\S]*?</JumpPoints>','<JumpPoints/>',scene);scene=re.sub(r'<BoomResInfo>[\s\S]*?</BoomResInfo>','<BoomResInfo/>',scene)
    put('maps/ice_chapter.xml',scene)
    cfg=get('script/pve/config.lua');assert hashlib.sha256(data['/script/pve/config.lua']).hexdigest()=='142515f07e84aaab3ba8ea4d4c2b2ba76a14b70d35abc40fc09996e04061d914'
    cfg=cfg.replace('maps={','maps={\r\n{8170,"act_ice_chapter"},',1)
    template=re.search(r'^\["喽罗甲"\].*$',cfg,re.M).group()
    custom=template.replace('喽罗甲','ice_guard').replace('"pve008"','"ice_guard_ai"')
    cfg=cfg.replace(template,template+'\r\n'+custom,1);put('script/pve/config.lua',cfg)
    ai=get('script/pve006');put('script/ice_guard_ai','-- Chapter 3: dedicated melee guard AI, based on original pve006.\r\n'+ai)
    script='''function main()
    map_blocks = {}
    local players = {
        {p={-175,84,-225},d=2}, {p={-155,84,-225},d=2},
        {p={-135,84,-225},d=2}, {p={-115,84,-225},d=2},
        {p={-175,84,-205},d=2}, {p={-115,84,-205},d=2}
    }
    local event_boxs = {
        {boxs={{-230,60,-270,-60,120,-90}},event=EVENT_GROUP({
            max=2,subs={{max=2,monsters={
                {n="ice_guard",p={-170,84,-150},d=6},
                {n="ice_guard",p={-120,84,-150},d=6},
                {n="ice_guard",p={-170,84,-150},d=6},
                {n="ice_guard",p={-120,84,-150},d=6}
            }}}
        })}
    }
    map_init(players, event_boxs, 8)
end
'''
    put('script/pve/act_ice_chapter.lua',script)
    result=pack(original,data);(out/'config.spf2').write_bytes(result)
    changed=[n for n,v in data.items() if before.get(n)!=v]
    for n in changed:
        p=out/'配置源码'/n.lstrip('/');p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(data[n])
    names=sorted(re.findall(rb'^\["[^"\r\n]+"\]\s*=\s*\{"([^"\r\n]+)"',data['/script/pve/config.lua'],re.M))
    idx=names.index(b'ice_guard')
    plan={'global_limit':8,'player_limit':6,'groups':[{'spawns':[{'template':idx,'position':[x,84,-150],'direction':6} for x in [-170,-120,-170,-120]],'sub_limit':2,'group_limit':2,'trigger_box':[-230,60,-270,-60,120,-90]}]}
    meta={'source_sha256':hashlib.sha256(original).hexdigest(),'sha256':hashlib.sha256(result).hexdigest(),'template_config_hash':hashlib.sha256(data['/script/pve/config.lua']).hexdigest(),'script_hash':hashlib.sha256(data['/script/pve/act_ice_chapter.lua']).hexdigest(),'new_template_index':idx,'plan':plan,'changed':changed}
    (out/'build.json').write_text(json.dumps(meta,ensure_ascii=False,indent=2),encoding='utf-8')
    assert source.read_bytes()==original
    print(json.dumps(meta,ensure_ascii=True))

if __name__=='__main__':build(Path(sys.argv[1]),Path(sys.argv[2]))
