"""Fixed realm-2 operator connection. SSH key stays on the operator's machine."""
import hashlib
import json
import subprocess
import tempfile
from pathlib import Path
from types import SimpleNamespace

BASE = 'https://openkfo.oss-cn-hangzhou.aliyuncs.com/'
FIXED = {'bucket': 'openkfo', 'region': 'cn-hangzhou', 'base': BASE}
DEFAULT_KEY = r'E:\ams\kongfukid\bieren\kk.pem'


class RealmSync:
    def __init__(self, key, notify, allow_restart=False):
        self.key, self.notify, self.allow_restart = key, notify, allow_restart
        self.request = None

    def call(self, request):
        if not Path(self.key).is_file():
            raise ValueError('请选择二区 SSH 私钥；私钥不会进入更新包或上传 OSS。')
        command = ['ssh', '-T', '-i', str(Path(self.key).resolve()), '-o', 'BatchMode=yes',
                   '-o', 'IdentitiesOnly=yes', '-o', 'StrictHostKeyChecking=yes',
                   '-o', 'ConnectTimeout=10', '-o', 'ServerAliveInterval=10',
                   '-o', 'ServerAliveCountMax=2', 'root@47.122.124.138', '/opt/kungfu-go/oss-sync-helper']
        try:
            result = subprocess.run(command, input=json.dumps(request).encode(), capture_output=True,
                                    timeout=240, creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0))
        except subprocess.TimeoutExpired:
            raise RuntimeError('二区同步响应超时，执行结果未确认；请读取同步状态，不要重复发布。') from None
        if result.returncode:
            error = result.stderr.decode('utf-8', errors='replace').strip()[-2000:]
            raise RuntimeError('二区同步未完成：' + error)
        response = json.loads(result.stdout)
        if response.get('realm') != '二区':
            raise ValueError('服务器身份不是二区，停止发布')
        return response

    def status(self):
        return self.call({'mode': 'status'})

    def prepare(self, release, store):
        """Called after object verification, before exposing the new OSS pointer."""
        key = f'manifest/{release.version}/client.json'
        raw = (release.root / key).read_bytes()
        rows = json.loads(raw)['files']
        configs = [r for r in rows if r['path'].lower() == 'data/config.spf2']
        if len(configs) != 1:
            raise ValueError('一键同步需要唯一的 Data/config.spf2；请保留历史客户端清单。')
        expected = configs[0]['sha256']
        launcher = json.loads((release.root / f'manifest/{release.version}/launcher.json').read_bytes())
        bridges = [r for r in launcher['files'] if r['path'].lower().endswith('/bridge.json') or r['path'].lower() == 'bridge.json']
        if not bridges:
            raise ValueError('更新包缺少登录组件 bridge.json，无法确认客户端配置哈希。')
        for row in bridges:
            if not row['url'].startswith(BASE):
                raise ValueError('登录组件不是固定 OSS 地址')
            blob = store.get(row['url'][len(BASE):])
            if blob is None or hashlib.sha256(blob).hexdigest() != row['sha256']:
                raise ValueError('登录组件文件校验失败')
            if json.loads(blob).get('config_hash') != expected:
                raise ValueError('bridge.json 与 config.spf2 哈希不同，请重新制作完整更新包；版本入口尚未发布。')
        self.request = {'version': release.version, 'manifest_hash': hashlib.sha256(raw).hexdigest(),
                        'allow_restart': self.allow_restart}
        self.notify('log', '正在预检二区配置兼容性和数据库绑定，版本入口尚未发布…')
        response = self.call(dict(self.request, mode='prepare'))
        if response.get('state') != 'prepared' or response.get('target_hash') != expected:
            raise ValueError('二区预检结果与更新包不一致')
        self.notify('log', '二区预检通过；' + ('配置变化，同步时将重启。' if response.get('restart_required') else '哈希相同，无需重启。'))

    def activate(self):
        if not self.request:
            raise ValueError('缺少二区发布预检结果')
        self.notify('log', 'OSS 已发布，正在同步二区；此阶段请勿关闭工具…')
        try:
            response = self.call(dict(self.request, mode='sync'))
            if response.get('state') != 'synced':
                raise ValueError('服务器未确认同步成功')
            self.notify('log', f"二区同步完成：{response['config_hash']}；重启：{'是' if response['restarted'] else '否'}")
            return response
        except Exception as error:
            raise RuntimeError(f'OSS 已发布，但二区同步未完成。不要改版本号重发；可用“重试同步当前版本”。\n{error}') from error

    def retry(self, store):
        pointer = json.loads(store.get('version/version.json'))
        version = pointer['version']
        url = pointer.get('client_manifest', '')
        if url != BASE + f'manifest/{version}/client.json':
            raise ValueError('当前客户端清单地址无效')
        if pointer.get('manifest') != BASE + f'manifest/{version}/launcher.json':
            raise ValueError('当前启动器清单地址无效')
        with tempfile.TemporaryDirectory(prefix='OpenKFO-sync-') as temporary:
            root = Path(temporary)
            for target in ('client', 'launcher'):
                key = f'manifest/{version}/{target}.json'
                # The exact expected URL prevents path traversal via version.
                from publisher import safe_key
                safe_key(key)
                path = root / key
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(store.get(key))
            self.prepare(SimpleNamespace(root=root, version=version), store)
            return self.activate()
