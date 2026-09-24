"""Build-time thumbnails from original RenderWare DFFs and GM-decoded textures.
No game process or installed client file is modified. Requires bundled numpy/Pillow.
"""
from pathlib import Path
import io, struct, subprocess, tempfile
import numpy as np
from PIL import Image

ROOT = Path(__file__).resolve().parents[3]
DATA = ROOT / 'client/Data'
OUT = Path(__file__).with_name('previews')
GO = ROOT / 'server/runtime-local/go-sdk/go/bin/go.exe'
decoder = (ROOT / 'server/server/go-server/internal/desktop/texture.go').read_text(encoding='utf-8')
decoder = decoder[decoder.index('func decodeTexture'):decoder.index('func shopImages')]
cache = {}

def texture(name):
    if name in cache: return cache[name]
    # Reuse the current GM decoder verbatim; keep its ambiguity/CRC checks.
    source = 'package main\nimport("bytes";"compress/zlib";"encoding/binary";"fmt";"hash/crc32";"image/png";"io";"os")\n' + decoder
    source += '\nfunc main(){b,e:=os.ReadFile(os.Args[1]);if e!=nil{panic(e)};p,e:=decodeTexture(b);if e!=nil{panic(e)};if e=os.WriteFile(os.Args[2],p,0600);e!=nil{panic(e)}}'
    with tempfile.TemporaryDirectory(prefix='npc-preview-') as tmp:
        p=Path(tmp);(p/'decode.go').write_text(source,encoding='utf-8')
        subprocess.run([str(GO),'run',str(p/'decode.go'),str(DATA/'Role/Texture'/f'{name}.png'),str(p/'out.png')],check=True)
        cache[name]=np.array(Image.open(p/'out.png').convert('RGBA'))
    return cache[name]

def chunks(b,start=0,end=None):
    end=len(b) if end is None else end
    while start<end:
        kind,size,_=struct.unpack_from('<III',b,start);body=start+12
        assert body+size<=end
        yield kind,body,body+size
        start=body+size

def render(model,out):
    b=(DATA/'Role/Model/Bone'/f'{model}.dff').read_bytes()
    _,root,end=next(chunks(b));frames=[];geoms=[];atoms=[]
    for kind,a,z in chunks(b,root,end):
        if kind==0xe:
            _,s,_=next(chunks(b,a,z));n=struct.unpack_from('<I',b,s)[0]
            for i in range(n):
                raw=struct.unpack_from('<12fii',b,s+4+i*56);m=np.eye(4);m[:3,:3]=np.array(raw[:9]).reshape(3,3);m[3,:3]=raw[9:12]
                frames.append(m if raw[12]<0 else m@frames[raw[12]])
        if kind==0x14:
            _,s,_=next(chunks(b,a,z));atoms.append(struct.unpack_from('<II',b,s))
        if kind==0x1a:
            for g,ga,gz in chunks(b,a,z):
                if g!=0xf:continue
                parts=list(chunks(b,ga,gz));_,s,_=parts[0];flags,nt,nv,nm=struct.unpack_from('<4I',b,s)
                assert not flags&0x1000000 and nm==1 and flags&0x10 and flags&4
                u=s+16+nv*4;uv=np.frombuffer(b,'<f4',nv*2,u).reshape(-1,2).copy();u+=nv*8
                triangles=np.frombuffer(b,'<u2',nt*4,u).reshape(-1,4).copy();u+=nt*8+24
                v=np.frombuffer(b,'<f4',nv*3,u).reshape(-1,3).copy();materials=[]
                for k,ma,mz in parts:
                    if k!=8:continue
                    for t,ta,tz in chunks(b,ma,mz):
                        if t!=7:continue
                        tex=None
                        for q,qa,qz in chunks(b,ta,tz):
                            if q==6:
                                names=[b[na:nz].split(b'\0')[0].decode('ascii') for typ,na,nz in chunks(b,qa,qz) if typ==2]
                                if names and names[0]:tex=texture(names[0])
                        assert tex is not None
                        materials.append(tex)
                geoms.append((v,uv,triangles,materials))
    meshes=[]
    for frame,geom in atoms:
        v,uv,tris,mats=geoms[geom];v=(np.column_stack((v,np.ones(len(v))))@frames[frame])[:,:3]
        # Use the game character frame with world Y up. Slight three-quarter view.
        yaw=.35;c=np.cos(yaw);s=np.sin(yaw)
        v=v@np.array([[c,0,-s],[0,1,0],[s,0,c]])
        meshes.append((v[:,[0,1,2]],uv,tris,mats))
    allv=np.concatenate([m[0] for m in meshes]);lo=allv.min(0);hi=allv.max(0)
    scale=min(280/(hi[0]-lo[0]),280/(hi[1]-lo[1]));center=(lo+hi)/2
    canvas=np.full((320,320,4),[241,246,246,255],dtype=np.uint8);depth=np.full((320,320),-np.inf)
    for v,uv,tris,mats in meshes:
        v=(v-center)*scale;v[:,0]+=160;v[:,1]=160-v[:,1]
        for tri in tris:
            ids=tri[[1,0,3]];p=v[ids];t=uv[ids];tex=mats[tri[2]]
            x0,y0=np.maximum(np.floor(p[:,:2].min(0)).astype(int),0);x1,y1=np.minimum(np.ceil(p[:,:2].max(0)).astype(int),319)
            if x1<x0 or y1<y0:continue
            yy,xx=np.mgrid[y0:y1+1,x0:x1+1];x=xx+.5;y=yy+.5
            den=(p[1,1]-p[2,1])*(p[0,0]-p[2,0])+(p[2,0]-p[1,0])*(p[0,1]-p[2,1])
            if abs(den)<1e-5:continue
            w0=((p[1,1]-p[2,1])*(x-p[2,0])+(p[2,0]-p[1,0])*(y-p[2,1]))/den
            w1=((p[2,1]-p[0,1])*(x-p[2,0])+(p[0,0]-p[2,0])*(y-p[2,1]))/den;w2=1-w0-w1
            z=w0*p[0,2]+w1*p[1,2]+w2*p[2,2];mask=(w0>=0)&(w1>=0)&(w2>=0)&(z>depth[yy,xx])
            coords=w0[...,None]*t[0]+w1[...,None]*t[1]+w2[...,None]*t[2]
            tx=np.clip((coords[:,:,0]*tex.shape[1]).astype(int),0,tex.shape[1]-1);ty=np.clip((coords[:,:,1]*tex.shape[0]).astype(int),0,tex.shape[0]-1)
            color=tex[ty,tx];mask &=color[:,:,3]>100;canvas[yy[mask],xx[mask]]=color[mask];depth[yy[mask],xx[mask]]=z[mask]
    Image.fromarray(canvas).convert('RGB').save(out)

if __name__=='__main__':
    OUT.mkdir(exist_ok=True)
    for model,name in [('cha_05_014','egun'),('cha_03_01','bear')]:
        render(model,OUT/f'{name}.bmp');print(name)
