"""Validated OSS release upload. No network calls during ZIP inspection."""
import hashlib
import json
import re
import shutil
import stat
import threading
import zipfile
from dataclasses import dataclass, field
from pathlib import Path
from urllib.parse import unquote, urlsplit

POINTER = 'version/version.json'
MAX_FILE = 256 * 1024 * 1024
MAX_ARCHIVE = 1024 * 1024 * 1024
MAX_METADATA = 256 * 1024


def unwrap_error(error):
    from alibabacloud_oss_v2.exceptions import OperationError, RequestError, ResponseError
    while isinstance(error, (OperationError, RequestError, ResponseError)):
        inner = error.unwrap()
        if inner is None or inner is error:
            break
        error = inner
    return error


def error_message(error):
    error = unwrap_error(error)
    if hasattr(error, 'request_id'):
        advice = {
            'AccessDenied': '当前凭据没有访问权限。请检查目标 Bucket 的 RAM/Bucket 授权；公共读不包含上传权限，需要 oss:PutObject，回读校验需要 oss:GetObject。',
            'InvalidAccessKeyId': 'AccessKey ID 无效。请检查是否填错、已删除，或不属于当前阿里云账号。',
            'SignatureDoesNotMatch': '签名不匹配。请检查 AccessKey ID 与 Secret 是否为同一对。',
        }.get(error.code, 'OSS 请求失败。')
        return f'{advice}\nHTTP {error.status_code}，{error.code}，RequestId={error.request_id}'
    return str(error)


def digest(path):
    with Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def safe_key(value):
    if not isinstance(value, str) or not value or any(c in value for c in '\\:\x00'):
        raise ValueError('文件路径无效')
    for part in value.split('/'):
        if part in ('', '.', '..') or part.endswith((' ', '.')) or any(ord(c) < 32 for c in part):
            raise ValueError('压缩包包含不安全路径')
        if re.fullmatch(r'(?i)(con|prn|aux|nul|com[1-9]|lpt[1-9])', part.split('.')[0]):
            raise ValueError('压缩包包含 Windows 保留名称')
    return value


def read_json(path, limit=1024 * 1024):
    if path.stat().st_size > limit:
        raise ValueError('JSON 文件过大')
    value = json.loads(path.read_text(encoding='utf-8-sig'))
    if not isinstance(value, dict):
        raise ValueError('JSON 顶层必须是对象')
    return value


def announcement_bytes(title, content):
    if not isinstance(title, str) or not isinstance(content, str) or not content.strip():
        raise ValueError('公告内容不能为空')
    data = json.dumps({'title': title.strip(), 'content': content.strip()}, ensure_ascii=False, indent=2).encode('utf-8')
    if len(data) > 65536:
        raise ValueError('公告超过 64 KB')
    return data


@dataclass
class Release:
    root: Path
    version: str
    keys: list[str]
    notes: str
    references: dict = field(default_factory=dict)


def inspect_zip(archive, destination, base_url, cancel=None):
    cancel = cancel or threading.Event()
    destination = Path(destination)
    base = urlsplit(base_url.rstrip('/') + '/')
    if base.scheme != 'https' or not base.netloc or base.username or base.query or base.fragment:
        raise ValueError('下载地址必须是无账号、无参数的 HTTPS 地址')

    def object_key(url):
        parsed = urlsplit(url)
        if (parsed.scheme, parsed.netloc) != (base.scheme, base.netloc) or parsed.query or parsed.fragment:
            raise ValueError('更新清单下载地址与界面配置不一致')
        path = unquote(parsed.path)
        prefix = unquote(base.path)
        if not path.startswith(prefix):
            raise ValueError('更新文件超出下载地址目录')
        return safe_key(path[len(prefix):])

    with zipfile.ZipFile(archive) as z:
        entries = z.infolist()
        if len(entries) > 20000 or sum(i.file_size for i in entries) > MAX_ARCHIVE:
            raise ValueError('压缩包文件数量或解压体积超限')
        candidates = [i.filename[:-len(POINTER)] for i in entries
                      if not i.is_dir() and (i.filename == POINTER or i.filename.endswith('/' + POINTER))]
        if len(candidates) != 1:
            raise ValueError('请选择 OSS 上传包 ZIP，必须包含唯一的 version/version.json；普通启动器 ZIP 不能直接发布')
        prefix = candidates[0]
        seen = set()
        for item in entries:
            safe_key(item.filename.rstrip('/'))
            if item.is_dir():
                continue
            if not item.filename.startswith(prefix) or stat.S_ISLNK(item.external_attr >> 16):
                raise ValueError('压缩包包含根目录外文件或链接')
            key = safe_key(item.filename[len(prefix):])
            if key.casefold() in seen or item.file_size > MAX_FILE or item.flag_bits & 1:
                raise ValueError('压缩包包含重复、过大或加密文件')
            seen.add(key.casefold())
            if cancel.is_set():
                raise InterruptedError('已取消，未发布版本')
            target = destination / key
            target.parent.mkdir(parents=True, exist_ok=True)
            with z.open(item) as source, target.open('xb') as output:
                shutil.copyfileobj(source, output, 1024 * 1024)

    pointer = read_json(destination / POINTER, 262144)
    version = pointer.get('version', '')
    if not isinstance(version, str) or not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]{0,79}', version):
        raise ValueError('版本号无效')
    required = {POINTER}
    references = {}
    notes = ''
    for field, target in [('manifest', 'launcher'), ('client_manifest', 'client')]:
        if field not in pointer and target == 'client':
            continue
        key = object_key(pointer[field])
        if key != f'manifest/{version}/{target}.json':
            raise ValueError('清单路径与版本号不一致')
        manifest = read_json(destination / key)
        if manifest.get('version') != version or manifest.get('target') != target or not isinstance(manifest.get('files'), list):
            raise ValueError('更新清单格式或版本无效')
        required.add(key)
        if target == 'launcher':
            notes = manifest.get('notes', '')
        names = set()
        total = 0
        if not 1 <= len(manifest['files']) <= (1024 if target == 'launcher' else 16384):
            raise ValueError('清单文件数量超限')
        for row in manifest['files']:
            name = safe_key(row['path'])
            file_key = object_key(row['url'])
            if name.casefold() in names or not re.fullmatch(r'releases/[A-Za-z0-9][A-Za-z0-9._-]{0,79}/' + target + '/' + re.escape(name), file_key):
                raise ValueError('文件重复或资源路径与清单不一致')
            names.add(name.casefold())
            path = destination / file_key
            if not isinstance(row['size'], int) or not 0 < row['size'] <= MAX_FILE or not re.fullmatch('[0-9a-f]{64}', row['sha256']):
                raise ValueError('文件大小或 SHA-256 无效')
            expected = (row['size'], row['sha256'])
            if path.is_file():
                if (path.stat().st_size, digest(path)) != expected:
                    raise ValueError(f'文件校验失败：{name}')
                required.add(file_key)
            elif file_key.startswith(f'releases/{version}/'):
                raise ValueError(f'本次新增文件缺失：{name}')
            else:
                if file_key in references and references[file_key] != expected:
                    raise ValueError('引用资源校验信息冲突')
                references[file_key] = expected
            total += row['size']
        if total > 512 * 1024 * 1024:
            raise ValueError('更新清单超过客户端支持的 512 MB')
        if target == 'launcher' and not {'启动器.exe', 'launchersupport.exe', 'flutter_windows.dll', 'data/app.so'} <= names:
            raise ValueError('启动器清单缺少必要文件')
    updater = f'updater/{version}/updater.exe'
    if (destination / updater).exists():
        if digest(destination / updater) != digest(destination / f'releases/{version}/launcher/LauncherSupport.exe'):
            raise ValueError('更新辅助程序不一致')
        required.add(updater)
    if (destination / 'announcement.json').exists():
        notice = read_json(destination / 'announcement.json', 65536)
        announcement_bytes(notice.get('title', ''), notice.get('content'))
        required.add('announcement.json')
    actual = {p.relative_to(destination).as_posix() for p in destination.rglob('*') if p.is_file()}
    if required != actual:
        raise ValueError('压缩包包含清单未声明的文件，已停止发布')
    # The optional announcement is deliberately NOT published with a release.
    keys = sorted(required - {POINTER, 'announcement.json'}, key=lambda key: (key.startswith('manifest/'), key))
    return Release(destination, version, keys, notes, references)


def set_release_notes(release, notes):
    if not isinstance(notes, str) or not notes.strip() or len(notes.encode('utf-8')) > 8000:
        raise ValueError('版本更新说明不能为空，且最多 8000 字节')
    notes = notes.strip()
    for target in ('launcher', 'client'):
        path = release.root / 'manifest' / release.version / (target + '.json')
        if path.is_file():
            manifest = read_json(path)
            manifest['notes'] = notes
            path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding='utf-8')
    release.notes = notes


class OssStore:
    def __init__(self, bucket, region, access_id, secret, token=''):
        import alibabacloud_oss_v2 as oss
        if not re.fullmatch(r'[a-z0-9][a-z0-9-]{1,61}[a-z0-9]', bucket) or not re.fullmatch(r'[a-z0-9-]+', region):
            raise ValueError('Bucket 或地域格式无效')
        if not access_id.strip() or not secret.strip():
            raise ValueError('请填写 AccessKey ID 和 AccessKey Secret')
        self.oss, self.bucket = oss, bucket
        cfg = oss.config.load_default()
        cfg.region = region
        cfg.endpoint = f'https://oss-{region}.aliyuncs.com'
        cfg.credentials_provider = oss.credentials.StaticCredentialsProvider(access_id.strip(), secret.strip(), token.strip() or None)
        cfg.connect_timeout, cfg.readwrite_timeout = 10, 30
        cfg.retry_max_attempts = 3
        self.client = oss.Client(cfg)

    def get(self, key):
        try:
            result = self.client.get_object(self.oss.GetObjectRequest(bucket=self.bucket, key=key))
            with result.body as stream:
                data = bytearray()
                for chunk in stream.iter_bytes(block_size=64 * 1024):
                    if len(data) + len(chunk) > MAX_METADATA:
                        raise ValueError('远端版本或公告文件过大')
                    data.extend(chunk)
            return bytes(data)
        except Exception as error:
            cause = unwrap_error(error)
            if isinstance(cause, self.oss.exceptions.ServiceError) and cause.status_code == 404 and cause.code == 'NoSuchKey':
                return None
            raise

    def remote_hash(self, key):
        try:
            result = self.client.get_object(self.oss.GetObjectRequest(bucket=self.bucket, key=key))
            hash_value, size = hashlib.sha256(), 0
            with result.body as stream:
                for chunk in stream.iter_bytes(block_size=1024 * 1024):
                    size += len(chunk)
                    if size > MAX_FILE:
                        raise ValueError(f'远端文件过大：{key}')
                    hash_value.update(chunk)
            return size, hash_value.hexdigest()
        except Exception as error:
            cause = unwrap_error(error)
            if isinstance(cause, self.oss.exceptions.ServiceError) and cause.status_code == 404 and cause.code == 'NoSuchKey':
                return None
            raise

    def put(self, key, source, immutable=False, progress=None):
        request = self.oss.PutObjectRequest(bucket=self.bucket, key=key,
            content_type='application/json; charset=utf-8' if key.endswith('.json') else 'application/octet-stream',
            cache_control='no-cache' if not immutable else 'public, max-age=31536000, immutable',
            forbid_overwrite=immutable,
            progress_fn=(lambda _increment, done, total: progress(done, total)) if progress else None)
        if isinstance(source, Path):
            self.client.put_object_from_file(request, str(source))
        else:
            request.body = source
            self.client.put_object(request)


def retain_previous_client(release, store, previous, notify):
    """Carry forward client resources; current package wins for the same path."""
    if previous is None:
        return
    old_pointer = json.loads(previous)
    if not old_pointer.get('client_manifest'):
        return
    pointer = read_json(release.root / POINTER)
    suffix = f'manifest/{release.version}/launcher.json'
    if not pointer['manifest'].endswith(suffix):
        raise ValueError('启动器清单地址无效')
    base = pointer['manifest'][:-len(suffix)]
    url = old_pointer['client_manifest']
    if not url.startswith(base):
        raise ValueError('线上客户端清单与当前 OSS 地址不一致')
    key = safe_key(unquote(url[len(base):]))
    raw = store.get(key)
    if raw is None:
        raise ValueError('线上客户端清单不存在，未发布版本')
    old = json.loads(raw)
    if old.get('target') != 'client' or old.get('version') != old_pointer.get('version'):
        raise ValueError('线上客户端清单版本不一致')
    client_key = f'manifest/{release.version}/client.json'
    current = read_json(release.root / client_key) if pointer.get('client_manifest') else dict(old)
    rows = {r['path'].casefold(): r for r in old['files']}
    if len(rows) != len(old['files']):
        raise ValueError('线上客户端清单有重复路径')
    incoming = {r['path'].casefold(): r for r in current['files']}
    added = sorted(r['path'] for k, r in incoming.items() if k not in rows)
    replaced = sorted(r['path'] for k, r in incoming.items() if k in rows and
                      (r['size'], r['sha256']) != (rows[k]['size'], rows[k]['sha256']))
    retained = sorted(r['path'] for k, r in rows.items() if k not in incoming)
    count = len(retained)
    rows.update({r['path'].casefold(): r for r in current['files']})
    current.update(version=release.version, notes=release.notes, files=list(rows.values()))
    pointer['client_manifest'] = base + client_key
    replacements = {client_key: json.dumps(current, ensure_ascii=False, indent=2).encode(),
                    POINTER: json.dumps(pointer, ensure_ascii=False, indent=2).encode()}
    # Reuse the complete package validator before changing the staged manifest.
    import tempfile
    with tempfile.TemporaryDirectory() as temporary:
        temporary = Path(temporary)
        archive = temporary / 'merged.zip'
        with zipfile.ZipFile(archive, 'w') as z:
            for path in release.root.rglob('*'):
                if path.is_file():
                    name = path.relative_to(release.root).as_posix()
                    if name not in replacements:
                        z.write(path, name)
            for name, data in replacements.items():
                z.writestr(name, data)
        validated = inspect_zip(archive, temporary / 'check', base)
    for name, data in replacements.items():
        path = release.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
    release.keys, release.references = validated.keys, validated.references
    notify('log', f'已自动保留旧版客户端资源 {count} 项；同路径使用本次版本，支持跨版本补齐')
    notify('package', f'合并后客户端清单：{len(rows)} 项 · 继承旧文件 {count} 项 · 新增 {len(added)} 项 · 内容替换 {len(replaced)} 项')
    for label, paths in [('继承', retained), ('新增', added), ('替换', replaced)]:
        for path in paths:
            notify('log', f'{label}：{path}')


def publish(release, store, notify, cancel, backup_dir):
    previous = store.get(POINTER)
    backup_dir = Path(backup_dir)
    backup_dir.mkdir(parents=True, exist_ok=True)
    if previous is not None:
        import time
        (backup_dir / f'version-before-{time.time_ns()}.json').write_bytes(previous)
    retain_previous_client(release, store, previous, notify)
    # A sparse package may reuse immutable objects from an earlier release.
    # Verify every dependency before uploading or changing the public pointer.
    for key, expected in release.references.items():
        if cancel.is_set():
            raise InterruptedError('已取消，未发布版本')
        notify('log', f'核对已有资源（不重新上传）：{key}')
        if store.remote_hash(key) != expected:
            raise ValueError(f'已有资源缺失或校验不符：{key}；未发布版本')
    for index, key in enumerate(release.keys, 1):
        if cancel.is_set():
            raise InterruptedError('已取消，未发布版本入口；已上传资源可在重试时复用')
        path = release.root / key
        expected = (path.stat().st_size, digest(path))
        notify('file', (index, len(release.keys), key))
        existing = store.remote_hash(key)
        if existing is not None:
            if existing != expected:
                raise ValueError(f'远端同版本文件内容不同：{key}。请更换版本号，不覆盖已发布资源')
            notify('log', f'已存在且校验一致，跳过：{key}')
        else:
            store.put(key, path, immutable=True, progress=lambda done, total: notify('progress', (done, total)))
            notify('log', f'回读校验：{key}')
            if store.remote_hash(key) != expected:
                raise ValueError(f'上传后 SHA-256 校验失败：{key}')
        notify('progress', (expected[0], expected[0]))
    if cancel.is_set():
        raise InterruptedError('已取消，未发布版本入口')
    if store.get(POINTER) != previous:
        raise ValueError('上传期间线上版本已改变，停止发布；请检查是否有其他发布者')
    notify('committing', '资源校验完成，正在发布版本入口（此阶段不可取消）')
    data = (release.root / POINTER).read_bytes()
    try:
        store.put(POINTER, data)
    except Exception:
        # A timeout may occur after OSS accepted the write. Read before reporting.
        if store.get(POINTER) != data:
            raise RuntimeError('版本入口发布结果未确认，请检查线上版本后再操作') from None
    if store.get(POINTER) != data:
        raise RuntimeError('版本入口回读不一致，请检查线上版本；不要直接回滚其他人的发布')
    notify('log', f'发布成功：{release.version}')
