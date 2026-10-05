"""Build an offline-complete Flutter launcher and per-file update feed. Never deploys."""
import argparse, base64, hashlib, json, os, shutil, subprocess, tempfile, zipfile
from pathlib import Path
root=Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser();p.add_argument('--version',default='2026.09.21-flutter.1');p.add_argument('--flutter',default='E:/OpenKFO-Flutter3169/flutter/bin/flutter.bat');p.add_argument('--config-hash',required=True);p.add_argument('--realms-config');p.add_argument('--launcher-version-url');p.add_argument('--server-version-url',help='game-server update entry tried before OSS');args=p.parse_args()
assert len(args.config_hash)==64 and all(c in '0123456789abcdef' for c in args.config_hash),'Expected client config SHA256'
def run(argv,cwd):
    subprocess.run([str(x) for x in argv],cwd=cwd,check=True)
def digest(path):
    return hashlib.file_digest(path.open('rb'),'sha256').hexdigest()
source=root/'launcher/launcher-flutter'
stage=Path(tempfile.mkdtemp(prefix='openkfo-flutter-build-'))
shutil.copytree(source,stage,dirs_exist_ok=True,ignore=shutil.ignore_patterns('build','.dart_tool','ephemeral','.git'))
flutter=args.flutter;assert Path(flutter).is_file(),'Flutter 3.16.9 SDK missing'
version=json.loads(subprocess.check_output([flutter,'--version','--machine'],text=True,encoding='utf-8'))
assert version['frameworkVersion']=='3.16.9','Use Flutter 3.16.9 for Windows 7 compatibility'
run([flutter,'pub','get'],stage)
run([flutter,'analyze','--no-pub'],stage)
run([flutter,'test','--no-pub'],stage)
run([flutter,'build','windows','--release','--no-pub',f'--dart-define=LAUNCHER_VERSION={args.version}']+([f'--dart-define=SERVER_VERSION_URL={args.server_version_url}'] if args.server_version_url else []),stage)
out=root/'dist'/('launcher-flutter-'+args.version)
if out.exists():raise SystemExit('Use a new version: output already exists')
release_dir=stage/'build/windows/x64/runner/Release'
if not release_dir.is_dir(): release_dir=stage/'build/windows/runner/Release'
shutil.copytree(release_dir,out)
(out/'openkfo_launcher.exe').rename(out/'启动器.exe')
for f in out.rglob('*.pdb'):f.unlink()
go=root/'runtime-local/go120/go/bin/go.exe'
os.environ.update(GOOS='windows',GOARCH='amd64',CGO_ENABLED='0')
run([go,'test','-mod=readonly','-modfile=bridge-go120.mod','./cmd/launcher-support'],root/'server/go-server')
run([go,'build','-mod=readonly','-modfile=bridge-go120.mod','-trimpath','-ldflags=-s -w -H=windowsgui','-o',out/'LauncherSupport.exe','./cmd/launcher-support'],root/'server/go-server')
# Runner and plugins use the static CRT; inspect engine imports in release QA.
run(['powershell.exe','-NoProfile','-ExecutionPolicy','Bypass','-File',root/'tools/Build-BridgeGo120.ps1'],root)
payload=out/'launcher-files';payload.mkdir()
for name in ['LoginSkin.dll','LoginSkinHost.exe','SDError.dll','lqbz.dll','libssl-1_1.dll','libcrypto-1_1.dll','OnlineBridge.exe']:
    shutil.copy2(root/'dist/launcher-components'/name,payload/name)
runtime=payload/'runtime-x86';runtime.mkdir()
vc=root/'runtime-local/vc2019-minimum/vcruntime140.dll'
assert vc.exists(),'Extract the signed Microsoft VS2019 x86 redistributable first'
shutil.copy2(vc,runtime/vc.name)
ucrt=Path('C:/Program Files (x86)/Windows Kits/10/Redist/ucrt/DLLs/x86')
assert (ucrt/'ucrtbase.dll').exists(),'Windows 7 compatible app-local UCRT missing'
for dll in ucrt.glob('*.dll'):shutil.copy2(dll,runtime/dll.name)
shutil.copy2(root/'launcher/launcher-online/Resources/client-config.xml',payload/'client-config.xml')
with zipfile.ZipFile(root/'runtime-local/launcher-package/bootstrap.zip') as z:
    allowed={'bridge.json','launcher-certificates/online/origin.crt','launcher-certificates/online/login.crt','launcher-certificates/online/login.key'}
    assert set(z.namelist())==allowed
    for name in allowed:
        target=payload/name;target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(z.read(name))
bridge=json.loads((payload/'bridge.json').read_text(encoding='utf-8-sig'))
bridge['config_hash']=args.config_hash
if args.realms_config:
    profiles_path=Path(args.realms_config).resolve()
    profiles=json.loads(profiles_path.read_text(encoding='utf-8-sig'))
    assert isinstance(profiles,list) and profiles
    for profile in profiles:
        certificate=profile['server_certificate']
        assert not Path(certificate).is_absolute() and '..' not in Path(certificate).parts
        if not (payload/certificate).exists():
            target=payload/certificate;target.parent.mkdir(parents=True,exist_ok=True)
            shutil.copy2(profiles_path.parent/certificate,target)
    (payload/'realms.json').write_text(json.dumps(profiles,ensure_ascii=False,indent=2),encoding='utf-8')
if args.launcher_version_url:
    assert args.launcher_version_url.startswith('https://')
    bridge['launcher_update_version_url']=args.launcher_version_url
embedded=json.loads((source/'lib/embedded_certificates.dart').read_text(encoding='utf-8').split('= <String, String>',1)[1].strip().removesuffix(';'))
required=[bridge[k].replace('\\','/').removeprefix('launcher-files/') for k in ['server_certificate','login_certificate','login_key']]
if args.realms_config: required += [profile['server_certificate'] for profile in profiles]
use_embedded=all(name in embedded and (payload/name).is_file() and (payload/name).read_bytes()==base64.b64decode(embedded[name]) for name in required)
bridge['embedded_certificates']=use_embedded
if use_embedded:
    for name in embedded:
        file=payload/name
        if file.is_file() and file.read_bytes()==base64.b64decode(embedded[name]): file.unlink()
(payload/'bridge.json').write_text(json.dumps(bridge,ensure_ascii=False,indent=2),encoding='utf-8')
(payload/'files.json').write_text(json.dumps({f.relative_to(payload).as_posix():digest(f) for f in payload.rglob('*') if f.is_file()},ensure_ascii=False),encoding='utf-8')
feed=root/'dist'/('launcher-flutter-feed-'+args.version);(feed/'files').mkdir(parents=True)
files={}
for f in out.rglob('*'):
    if not f.is_file():continue
    sha=digest(f);files[f.relative_to(out).as_posix()]={'sha256':sha,'size':f.stat().st_size}
    shutil.copy2(f,feed/'files'/(sha+'.bin'))
(feed/'launcher-flutter.json').write_text(json.dumps({'kind':'launcher-flutter','version':args.version,'notes':'Flutter 独立启动器；自动准备登录组件和证书；不包含游戏客户端；按文件更新。','files':files},ensure_ascii=False,indent=2),encoding='utf-8')
zip_path=out.with_suffix('.zip')
# Keep version dots in the directory name; with_suffix would truncate the version.
zip_path=Path(str(out)+'.zip')
with zipfile.ZipFile(zip_path,'w',zipfile.ZIP_DEFLATED,compresslevel=9) as z:
    for f in out.rglob('*'):
        if f.is_file():z.write(f,f.relative_to(out))
print(json.dumps({'directory':str(out),'zip':str(zip_path),'zip_bytes':zip_path.stat().st_size,'total_bytes':sum(f['size'] for f in files.values()),'exe_bytes':(out/'启动器.exe').stat().st_size,'logic_bytes':(out/'data/app.so').stat().st_size,'feed':str(feed)},ensure_ascii=False))
