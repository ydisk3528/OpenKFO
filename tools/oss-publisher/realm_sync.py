"""Fixed realm-3 operator connection. Password stays in the local DPAPI vault."""
import hashlib
import json
import paramiko
import base64
import socket
import tempfile
from pathlib import Path
from types import SimpleNamespace

BASE = 'https://openkfo.oss-cn-hangzhou.aliyuncs.com/'
FIXED = {'bucket': 'openkfo', 'region': 'cn-hangzhou', 'base': BASE}
SSH_HOST = 'aaa.vxfnqfjdr.top'
SSH_USER = 'root'
SSH_HOST_KEY = 'AAAAC3NzaC1lZDI1NTE5AAAAIBkoD0SsZPijRQeS0XVkLIxeDsbafXBQAJ2sQN1uydEc'


class RealmSync:
    def __init__(self, password, notify, allow_restart=False):
        self.password, self.notify, self.allow_restart = password, notify, allow_restart
        self.request = None

    def call(self, request):
        if not self.password:
            raise ValueError('请填写三区登录密码。')
        client = paramiko.SSHClient()
        client.get_host_keys().add(SSH_HOST, 'ssh-ed25519', paramiko.Ed25519Key(data=base64.b64decode(SSH_HOST_KEY)))
        client.set_missing_host_key_policy(paramiko.RejectPolicy())
        try:
            client.connect(SSH_HOST, username=SSH_USER, password=self.password,
                           look_for_keys=False, allow_agent=False, timeout=15, auth_timeout=15, banner_timeout=15)
            stdin, stdout, stderr = client.exec_command('/opt/kungfu-go/oss-sync-helper', timeout=240)
            stdin.write(json.dumps(request)); stdin.flush(); stdin.channel.shutdown_write()
            raw, error = stdout.read(), stderr.read()
            if stdout.channel.recv_exit_status():
                raise RuntimeError('三区同步未完成：' + error.decode('utf-8', 'replace')[-2000:])
            response = json.loads(raw)
        except paramiko.AuthenticationException:
            raise RuntimeError('三区登录失败，请检查服务器密码。') from None
        except paramiko.BadHostKeyException:
            raise RuntimeError('三区主机密钥不匹配，已停止连接。') from None
        except (socket.timeout, TimeoutError):
            raise RuntimeError('三区同步响应超时，执行结果未确认；请读取同步状态，不要重复发布。') from None
        finally:
            client.close()
        if response.get('realm') != '三区':
            raise ValueError('服务器身份不是三区，停止发布')
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
        self.notify('log', '正在预检三区配置兼容性和数据库绑定，版本入口尚未发布…')
        response = self.call(dict(self.request, mode='prepare'))
        if response.get('state') != 'prepared' or response.get('target_hash') != expected:
            raise ValueError('三区预检结果与更新包不一致')
        self.notify('log', '三区预检通过；' + ('配置变化，同步时将重启。' if response.get('restart_required') else '哈希相同，无需重启。'))

    def activate(self):
        if not self.request:
            raise ValueError('缺少三区发布预检结果')
        self.notify('log', 'OSS 已发布，正在同步三区；此阶段请勿关闭工具…')
        try:
            response = self.call(dict(self.request, mode='sync'))
            if response.get('state') != 'synced':
                raise ValueError('服务器未确认同步成功')
            self.notify('log', f"三区同步完成：{response['config_hash']}；重启：{'是' if response['restarted'] else '否'}")
            return response
        except Exception as error:
            raise RuntimeError(f'OSS 已发布，但三区同步未完成。不要改版本号重发；可用“重试同步当前版本”。\n{error}') from error

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
