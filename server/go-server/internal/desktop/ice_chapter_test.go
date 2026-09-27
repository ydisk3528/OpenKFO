package desktop

import (
 "os"
 "testing"
 "slices"
)

func TestIceChapterAndOriginalTemplateMapping(t *testing.T){
 base:=os.Getenv("OPENKFO_ICE_BASE");candidate:=os.Getenv("OPENKFO_ICE_CANDIDATE");if base==""||candidate==""{t.Skip("chapter archives required")}
 old,e:=ReadStageMaps(base);if e!=nil{t.Fatal(e)};now,e:=ReadStageMaps(candidate);if e!=nil{t.Fatal(e)}
 if len(now)!=len(old)+1{t.Fatal("catalog count")}
 for _,a:=range old {if a.MapType!=10{continue};i:=slices.IndexFunc(now,func(b StageMap)bool{return b.MapID==a.MapID});if i<0{t.Fatal(a.MapID)};b:=now[i]
  if a.FosterPreview==nil||b.FosterPreview==nil{t.Fatal("plan missing",a.MapID)}
  for gi,g:=range a.FosterPreview.Groups {for si,sp:=range g.Spawns {ns:=b.FosterPreview.Groups[gi].Spawns[si];if a.FosterTemplates.Names[sp.Template]!=b.FosterTemplates.Names[ns.Template]||a.FosterTemplates.InitialHP[sp.Template]!=b.FosterTemplates.InitialHP[ns.Template]{t.Fatal("original monster changed",a.MapID)}}}
 }
 i:=slices.IndexFunc(now,func(b StageMap)bool{return b.MapID==8170});if i<0{t.Fatal("chapter missing")};n:=now[i]
 if n.Group!=7||n.FosterPreview==nil||len(n.FosterPreview.Groups)!=1||len(n.FosterPreview.Groups[0].Spawns)!=4{t.Fatal("new plan incorrect")}
 a,e:=loadArchive(candidate);if e!=nil{t.Fatal(e)};if e=a.verify();e!=nil{t.Fatal(e)}
 b,e:=loadArchive(base);if e!=nil{t.Fatal(e)}
 for _,name:=range []string{"levelup.txt","item.txt","itemact.txt","acteffect.xml","maps/water3.xml"}{x,_:=a.raw(name);y,_:=b.raw(name);if string(x)!=string(y){t.Fatal("unrelated config changed",name)}}
}
