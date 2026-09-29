"""Serve launcher/client updates from the game server's own web root.

Objects keep the OSS layout (version/version.json, manifest/<v>/, releases/<v>/),
only the URLs point at this server. Runs on the server with the standard library:

  mirror    copy the current release from OSS (or another HTTPS source)
  publish   publish an upload ZIP made by Prepare-OssRelease.py --base-url <server>
  announce  replace announcement.json
  verify    check every object referenced by the current release
"""
import argparse
import fcntl
import hashlib
import json
import os
import re
import shutil
import sys
import tempfile
import threading
import urllib.error
import urllib.request
from contextlib import contextmanager
from pathlib import Path
from urllib.parse import quote, unquote, urlsplit

from publisher import (MAX_FILE, MAX_METADATA, POINTER, announcement_bytes, inspect_zip, publish,
                       safe_key, set_release_notes)

VERSION = r'[A-Za-z0-9][A-Za-z0-9._-]{0,79}'
MAX_MANIFEST = 1024 * 1024
DEFAULT_ROOT = '/var/www/kfo/dl'  # nginx: root /var/www/kfo; dl-state beside it is not served
DEFAULT_BASE = 'https://vxziouwkf.top/dl/'
DEFAULT_SOURCE = 'https://openkfo.oss-cn-hangzhou.aliyuncs.com/'


def normalize_base(value):
    parts = urlsplit(value)
    if parts.scheme != 'https' or not parts.netloc or parts.username or parts.query or parts.fragment:
        raise ValueError(f'地址必须是无账号、无参数的 HTTPS 地址：{value}')
    return value.rstrip('/') + '/'


def file_digest(path):
    with Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


class FileStore:
    """Directory-backed store with the OssStore get/remote_hash/put contract."""

    def __init__(self, root, staging):
        self.root, self.staging = Path(root), Path(staging)
        self.root.mkdir(parents=True, exist_ok=True)
        self.staging.mkdir(parents=True, exist_ok=True)

    def path(self, key):
        return self.root / safe_key(key)

    def get(self, key, limit=MAX_METADATA):
        path = self.path(key)
        if not path.is_file():
            return None
        if path.stat().st_size > limit:
            raise ValueError(f'文件过大：{key}')
        return path.read_bytes()

    def remote_hash(self, key):
        path = self.path(key)
        if not path.is_file():
            return None
        return path.stat().st_size, file_digest(path)

    def put(self, key, source, immutable=False, progress=None):
        target = self.path(key)
        if immutable and target.exists():
            raise FileExistsError(f'不覆盖已发布文件：{key}')
        # Staging shares the filesystem with root, so link/replace are atomic.
        fd, temp = tempfile.mkstemp(dir=self.staging)
        try:
            with os.fdopen(fd, 'wb') as output:
                if isinstance(source, Path):
                    with source.open('rb') as stream:
                        shutil.copyfileobj(stream, output, 1024 * 1024)
                else:
                    output.write(source)
                output.flush()
                os.fsync(output.fileno())
            os.chmod(temp, 0o644)
            target.parent.mkdir(parents=True, exist_ok=True)
            if immutable:
                os.link(temp, target)  # Fails instead of replacing a concurrent writer.
            else:
                os.replace(temp, target)
        finally:
            if os.path.exists(temp):
                os.unlink(temp)
        if progress:
            size = target.stat().st_size
            progress(size, size)


class HttpsOnlyRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        if urlsplit(newurl).scheme != 'https':
            raise ValueError(f'拒绝非 HTTPS 重定向：{newurl}')
        return super().redirect_request(req, fp, code, msg, headers, newurl)


class Fetcher:
    def __init__(self, timeout=60):
        self.timeout = timeout
        self.opener = urllib.request.build_opener(HttpsOnlyRedirect)

    def open(self, url):
        if urlsplit(url).scheme != 'https':
            raise ValueError(f'只允许 HTTPS 源地址：{url}')
        request = urllib.request.Request(url, headers={'Cache-Control': 'no-cache', 'User-Agent': 'OpenKFO-ServerMirror'})
        return self.opener.open(request, timeout=self.timeout)

    def bytes(self, url, limit):
        """Return the body, or None for HTTP 404."""
        try:
            with self.open(url) as response:
                data = response.read(limit + 1)
        except urllib.error.HTTPError as error:
            if error.code == 404:
                return None
            raise
        if len(data) > limit:
            raise ValueError(f'文件过大：{url}')
        return data

    def file(self, url, destination, limit):
        size = 0
        with self.open(url) as response, Path(destination).open('wb') as output:
            while chunk := response.read(1024 * 1024):
                size += len(chunk)
                if size > limit:
                    raise ValueError(f'文件过大：{url}')
                output.write(chunk)


def put_same(store, key, data):
    """Write an immutable object once; an identical existing copy is accepted."""
    existing = store.remote_hash(key)
    if existing is None:
        store.put(key, data, immutable=True)
    elif existing != (len(data), hashlib.sha256(data).hexdigest()):
        raise ValueError(f'服务器已有同版本清单但内容不同：{key}；请用新版本号发布')


def mirror(source, store, base, notify=print, switch=False, fetcher=None):
    """Copy the source's current release; files are verified by SHA-256 before use."""
    fetcher = fetcher or Fetcher()
    source, base = normalize_base(source), normalize_base(base)

    def key_of(url):
        if not isinstance(url, str) or not url.startswith(source):
            raise ValueError(f'地址不在源目录下：{url}')
        return safe_key(unquote(url[len(source):]))

    raw = fetcher.bytes(source + POINTER, MAX_METADATA)
    if raw is None:
        raise ValueError('源地址没有 version/version.json')
    pointer = json.loads(raw)
    version = pointer.get('version') if isinstance(pointer, dict) else None
    if not isinstance(version, str) or not re.fullmatch(VERSION, version):
        raise ValueError('源版本号无效')
    current = store.get(POINTER)
    if current is not None and json.loads(current).get('version') != version and not switch:
        raise ValueError(f'服务器当前版本 {json.loads(current).get("version")} 与源版本 {version} 不同；'
                         '确认要切换到源版本时加 --switch')
    result = {'version': version}
    for field, target in (('manifest', 'launcher'), ('client_manifest', 'client')):
        if pointer.get(field) is None and target == 'client':
            continue
        key = key_of(pointer.get(field))
        if key != f'manifest/{version}/{target}.json':
            raise ValueError('清单路径与版本号不一致')
        manifest = json.loads(fetcher.bytes(pointer[field], MAX_MANIFEST) or b'null')
        if not isinstance(manifest, dict) or manifest.get('version') != version or \
                manifest.get('target') != target or not isinstance(manifest.get('files'), list):
            raise ValueError(f'{target} 清单格式或版本无效')
        names = set()
        for row in manifest['files']:
            name, file_key = safe_key(row['path']), key_of(row['url'])
            if name.casefold() in names or not re.fullmatch(f'releases/{VERSION}/{target}/' + re.escape(name), file_key):
                raise ValueError(f'文件重复或资源路径与清单不一致：{name}')
            names.add(name.casefold())
            expected = (row['size'], row['sha256'])
            if not isinstance(row['size'], int) or not 0 < row['size'] <= MAX_FILE or \
                    not re.fullmatch('[0-9a-f]{64}', str(row['sha256'])):
                raise ValueError(f'文件大小或 SHA-256 无效：{name}')
            existing = store.remote_hash(file_key)
            if existing is None:
                notify(f'下载：{file_key}')
                fd, temp = tempfile.mkstemp(dir=store.staging)
                os.close(fd)
                try:
                    fetcher.file(row['url'], temp, row['size'])
                    if (os.path.getsize(temp), file_digest(temp)) != expected:
                        raise ValueError(f'下载校验失败：{file_key}')
                    store.put(file_key, Path(temp), immutable=True)
                finally:
                    os.unlink(temp)
            elif existing != expected:
                raise ValueError(f'服务器已有同路径文件但内容不同：{file_key}')
            row['url'] = base + quote(file_key, safe='/')
        put_same(store, key, json.dumps(manifest, ensure_ascii=False, indent=2).encode())
        result[field] = base + key
        notify(f'{target} 清单：{len(names)} 个文件已就绪')
    if store.get('announcement.json') is None:
        notice = fetcher.bytes(source + 'announcement.json', 65536)
        if notice is not None:
            value = json.loads(notice)
            store.put('announcement.json', announcement_bytes(value.get('title', ''), value.get('content')))
            notify('已复制公告')
    data = json.dumps(result, ensure_ascii=False, indent=2).encode()
    # Pointer last: players only see the release after every object exists.
    if store.get(POINTER) != data:
        store.put(POINTER, data)
    return verify(store, base)


def verify(store, base):
    """Check the current pointer, manifests and every referenced object."""
    base = normalize_base(base)

    def key_of(url):
        if not isinstance(url, str) or not url.startswith(base):
            raise ValueError(f'清单地址不属于本服务器：{url}')
        return safe_key(unquote(url[len(base):]))

    raw = store.get(POINTER)
    if raw is None:
        raise ValueError('服务器还没有发布版本')
    pointer = json.loads(raw)
    summary = {'version': pointer['version']}
    for field, target in (('manifest', 'launcher'), ('client_manifest', 'client')):
        if field not in pointer:
            continue
        manifest = json.loads(store.get(key_of(pointer[field]), MAX_MANIFEST) or b'null')
        if not isinstance(manifest, dict) or manifest.get('version') != pointer['version'] or manifest.get('target') != target:
            raise ValueError(f'{target} 清单缺失或版本不一致')
        for row in manifest['files']:
            if store.remote_hash(key_of(row['url'])) != (row['size'], row['sha256']):
                raise ValueError(f'文件缺失或校验不符：{row["path"]}')
        summary[target] = len(manifest['files'])
        if target == 'client':
            summary['config_hash'] = manifest.get('config_hash')
    return summary


def publish_zip(archive, store, base, notes=None, notify=print):
    events = {'log', 'package', 'committing'}
    with tempfile.TemporaryDirectory(dir=store.staging) as work:
        release = inspect_zip(archive, Path(work) / 'extract', normalize_base(base))
        if notes:
            set_release_notes(release, notes)
        publish(release, store, lambda kind, value: notify(value) if kind in events else None,
                threading.Event(), store.staging.parent / (store.staging.name + '-backups'))
    return verify(store, base)


@contextmanager
def locked(state):
    state.mkdir(parents=True, exist_ok=True)
    with (state / 'publish.lock').open('w') as handle:
        try:
            fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise SystemExit('另一个发布任务正在运行，请稍后再试') from None
        yield


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--root', type=Path, default=Path(DEFAULT_ROOT), help='nginx 对外提供的目录')
    parser.add_argument('--base-url', default=DEFAULT_BASE, help='玩家访问 root 的 HTTPS 地址')
    commands = parser.add_subparsers(dest='command', required=True)
    m = commands.add_parser('mirror', help='从 OSS 等 HTTPS 源复制当前版本')
    m.add_argument('--source', default=DEFAULT_SOURCE)
    m.add_argument('--switch', action='store_true', help='服务器已有不同版本时，确认切换到源版本')
    p = commands.add_parser('publish', help='发布 Prepare-OssRelease.py 生成的上传包 ZIP')
    p.add_argument('zip', type=Path)
    p.add_argument('--notes', help='覆盖更新说明')
    a = commands.add_parser('announce', help='替换公告')
    a.add_argument('--title', default='')
    a.add_argument('--content-file', type=Path, required=True, help='UTF-8 正文文件')
    commands.add_parser('verify', help='核对当前版本引用的全部文件')
    args = parser.parse_args(argv)
    state = args.root.parent / (args.root.name + '-state')
    store = FileStore(args.root, state / 'staging')
    try:
        if args.command == 'verify':
            summary = verify(store, args.base_url)
        else:
            with locked(state):
                if args.command == 'mirror':
                    summary = mirror(args.source, store, args.base_url, switch=args.switch)
                elif args.command == 'publish':
                    summary = publish_zip(args.zip, store, args.base_url, args.notes)
                else:
                    content = args.content_file.read_text(encoding='utf-8-sig')
                    store.put('announcement.json', announcement_bytes(args.title, content))
                    summary = json.loads(store.get('announcement.json'))
    except (ValueError, OSError, urllib.error.URLError) as error:
        print(f'失败：{error}', file=sys.stderr)
        return 1
    print(json.dumps(summary, ensure_ascii=False, indent=2))
    return 0


if __name__ == '__main__':
    sys.exit(main())
