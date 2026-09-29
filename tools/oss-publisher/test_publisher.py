import hashlib
import json
import tempfile
import threading
import unittest
import zipfile
from types import SimpleNamespace
from unittest.mock import Mock, MagicMock
from pathlib import Path

from publisher import POINTER, MAX_METADATA, OssStore, announcement_bytes, inspect_zip, publish, safe_key, error_message, set_release_notes, retain_previous_client

BASE = 'https://openkfo.oss-cn-hangzhou.aliyuncs.com/'


def fixture():
    files, rows = {}, []
    for name in ('启动器.exe', 'LauncherSupport.exe', 'flutter_windows.dll', 'data/app.so'):
        data = name.encode()
        key = 'releases/test.1/launcher/' + name
        files[key] = data
        rows.append({'path': name, 'url': BASE + key, 'size': len(data), 'sha256': hashlib.sha256(data).hexdigest()})
    manifest = 'manifest/test.1/launcher.json'
    files[manifest] = json.dumps({'version': 'test.1', 'target': 'launcher', 'notes': 'test', 'files': rows}).encode()
    files[POINTER] = json.dumps({'version': 'test.1', 'manifest': BASE + manifest}).encode()
    return files


class Store:
    def __init__(self):
        self.files = {POINTER: b'{}'}
        self.writes = []
        self.fail = None
        self.corrupt = False

    def get(self, key):
        return self.files.get(key)

    def remote_hash(self, key):
        data = self.files.get(key)
        return (len(data), hashlib.sha256(data).hexdigest()) if data is not None else None

    def put(self, key, source, immutable=False, progress=None):
        if self.fail == key:
            raise OSError('simulated disconnect')
        assert not immutable or key not in self.files
        data = source.read_bytes() if isinstance(source, Path) else source
        self.files[key] = b'corrupt' if self.corrupt else data
        self.writes.append(key)
        if progress:
            progress(len(data), len(data))


class PublisherTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def release(self, files=None, wrapper=''):
        path = self.root / 'update.zip'
        with zipfile.ZipFile(path, 'w') as z:
            for name, data in (files or fixture()).items():
                z.writestr(wrapper + name, data)
        return inspect_zip(path, self.root / 'extract', BASE)

    def send(self, release, store, notify=lambda *_: None, cancel=None):
        publish(release, store, notify, cancel or threading.Event(), self.root / 'backups')

    def test_publish_merges_old_client_and_current_wins(self):
        files = fixture()
        store = Store()
        old_rows = []
        for name, data in [('Data/old-map.bin', b'map'), ('Data/config.spf2', b'old-config')]:
            key = 'releases/old.1/client/' + name
            store.files[key] = data
            old_rows.append(dict(path=name, url=BASE+key, size=len(data), sha256=hashlib.sha256(data).hexdigest()))
        old_key = 'manifest/old.1/client.json'
        store.files[old_key] = json.dumps(dict(version='old.1', target='client', files=old_rows)).encode()
        store.files[POINTER] = json.dumps(dict(version='old.1', client_manifest=BASE+old_key)).encode()
        key = 'releases/test.1/client/Data/config.spf2'
        data = b'new-config'
        files[key] = data
        new_row = dict(path='Data/config.spf2', url=BASE+key, size=len(data), sha256=hashlib.sha256(data).hexdigest())
        manifest_key = 'manifest/test.1/client.json'
        files[manifest_key] = json.dumps(dict(version='test.1', target='client', files=[new_row])).encode()
        pointer = json.loads(files[POINTER]); pointer['client_manifest'] = BASE+manifest_key
        files[POINTER] = json.dumps(pointer).encode()
        release = self.release(files)
        self.send(release, store)
        result = json.loads(store.files[manifest_key])
        self.assertEqual({r['path']: r for r in result['files']}, {'Data/old-map.bin':old_rows[0], 'Data/config.spf2':new_row})
        self.assertNotIn('releases/old.1/client/Data/old-map.bin', store.writes)
        self.assertEqual(store.writes[-1], POINTER)
        # Retry is idempotent after the pointer already moved to this version.
        self.send(release, store)

    def test_three_sparse_releases_keep_history_and_preview_never_writes(self):
        store = Store()
        expected = {}
        for version, changes in [('a.1', {'SDError.dll': b'dll', 'lqbz.dll': b'dep'}),
                                 ('b.1', {'Data/Map/water_double/szlg.rws': b'map'}),
                                 ('c.1', {'Data/config.spf2': b'weapon', 'SDError.dll': b'new-dll'})]:
            files = {k.replace('test.1', version): v.replace(b'test.1', version.encode())
                     if k.endswith('.json') else v for k, v in fixture().items()}
            rows = []
            for name, data in changes.items():
                key = f'releases/{version}/client/{name}'
                files[key] = data
                row = dict(path=name, url=BASE+key, size=len(data), sha256=hashlib.sha256(data).hexdigest())
                rows.append(row)
                expected[name] = row
            client_key = f'manifest/{version}/client.json'
            files[client_key] = json.dumps(dict(version=version, target='client', files=rows)).encode()
            pointer = json.loads(files[POINTER])
            pointer['client_manifest'] = BASE + client_key
            files[POINTER] = json.dumps(pointer).encode()
            directory = self.root / version
            directory.mkdir()
            archive = directory / 'package.zip'
            with zipfile.ZipFile(archive, 'w') as z:
                for name, data in files.items():
                    z.writestr(name, data)
            release = inspect_zip(archive, directory/'extracted', BASE)
            before = dict(store.files)
            retain_previous_client(release, store, store.get(POINTER), lambda *_: None)
            self.assertEqual(store.files, before)
            self.send(release, store)
            result = json.loads(store.files[client_key])
            self.assertEqual({r['path']: r for r in result['files']}, expected)
        self.assertEqual(store.writes.count('releases/a.1/client/lqbz.dll'), 1)
        self.assertEqual(store.writes.count('releases/b.1/client/Data/Map/water_double/szlg.rws'), 1)

    def test_full_release_pointer_last_and_backup(self):
        release = self.release(wrapper='外层目录/')
        store = Store()
        self.send(release, store)
        self.assertEqual(store.writes[-1], POINTER)
        self.assertEqual(store.files[POINTER], (release.root / POINTER).read_bytes())
        self.assertEqual(next((self.root / 'backups').iterdir()).read_bytes(), b'{}')

    def sparse(self):
        files = fixture()
        key = 'releases/test.1/launcher/flutter_windows.dll'
        data = files.pop(key)
        old = 'releases/old.1/launcher/flutter_windows.dll'
        manifest = 'manifest/test.1/launcher.json'
        files[manifest] = files[manifest].replace(key.encode(), old.encode())
        return self.release(files), old, data

    def test_sparse_reuses_verified_old_object(self):
        release, old, data = self.sparse()
        store = Store()
        store.files[old] = data
        self.send(release, store)
        self.assertNotIn(old, store.writes)
        self.assertEqual(store.writes[-1], POINTER)

    def test_sparse_missing_or_corrupt_dependency_keeps_pointer(self):
        release, old, data = self.sparse()
        for existing in (None, b'wrong'):
            store = Store()
            if existing is not None:
                store.files[old] = existing
            with self.assertRaisesRegex(ValueError, '已有资源'):
                self.send(release, store)
            self.assertEqual(store.writes, [])

    def test_current_release_file_cannot_be_omitted(self):
        files = fixture()
        del files['releases/test.1/launcher/flutter_windows.dll']
        with self.assertRaisesRegex(ValueError, '新增文件缺失'):
            self.release(files)

    def test_edited_notes_are_published_without_changing_resources(self):
        release = self.release()
        before = (release.root / 'releases/test.1/launcher/data/app.so').read_bytes()
        set_release_notes(release, '调整武器伤害')
        store = Store()
        self.send(release, store)
        manifest = json.loads(store.files['manifest/test.1/launcher.json'])
        self.assertEqual(manifest['notes'], '调整武器伤害')
        self.assertEqual((release.root / 'releases/test.1/launcher/data/app.so').read_bytes(), before)
        self.assertNotIn('announcement.json', store.writes)

    def test_invalid_edited_notes_rejected(self):
        release = self.release()
        for text in (' ', 'a' * 8001):
            with self.assertRaises(ValueError):
                set_release_notes(release, text)

    def test_traversal_and_windows_aliases(self):
        for key in ('../x', 'a/../x', 'C:/x', '/x', 'a\\x', 'A/CON.txt', 'A/x.', 'A/x ', 'a//x'):
            with self.subTest(key=key), self.assertRaises(ValueError):
                safe_key(key)

    def test_corrupt_file_rejected(self):
        files = fixture()
        files['releases/test.1/launcher/data/app.so'] = b'wrong'
        with self.assertRaisesRegex(ValueError, '校验失败'):
            self.release(files)

    def test_extra_file_rejected(self):
        files = fixture()
        files['private.json'] = b'not for upload'
        with self.assertRaisesRegex(ValueError, '未声明'):
            self.release(files)

    def test_foreign_url_rejected(self):
        files = fixture()
        files[POINTER] = files[POINTER].replace(BASE.encode(), b'https://other.invalid/')
        with self.assertRaisesRegex(ValueError, '不一致'):
            self.release(files)

    def test_duplicate_case_rejected(self):
        files = fixture()
        files['releases/test.1/launcher/DATA/app.so'] = b'duplicate'
        with self.assertRaisesRegex(ValueError, '重复'):
            self.release(files)

    def test_failure_does_not_publish_and_retry_reuses_files(self):
        release = self.release()
        store = Store()
        store.fail = release.keys[1]
        with self.assertRaises(OSError):
            self.send(release, store)
        self.assertEqual(store.get(POINTER), b'{}')
        first = store.writes[0]
        store.fail = None
        self.send(release, store)
        self.assertEqual(store.writes.count(first), 1)
        self.assertEqual(store.writes[-1], POINTER)

    def test_remote_conflict_not_overwritten(self):
        release = self.release()
        store = Store()
        store.files[release.keys[0]] = b'other release'
        with self.assertRaisesRegex(ValueError, '内容不同'):
            self.send(release, store)
        self.assertEqual(store.writes, [])

    def test_upload_readback_failure_keeps_old_version(self):
        release = self.release()
        store = Store()
        store.corrupt = True
        with self.assertRaisesRegex(ValueError, 'SHA-256'):
            self.send(release, store)
        self.assertEqual(store.get(POINTER), b'{}')

    def test_cancel_keeps_old_version(self):
        release = self.release()
        store, cancel = Store(), threading.Event()
        def notify(kind, _):
            if kind == 'progress':
                cancel.set()
        with self.assertRaises(InterruptedError):
            self.send(release, store, notify, cancel)
        self.assertEqual(store.get(POINTER), b'{}')

    def test_concurrent_version_change_stops_publish(self):
        release, store = self.release(), Store()
        def notify(kind, _):
            if kind == 'progress':
                store.files[POINTER] = b'another publisher'
        with self.assertRaisesRegex(ValueError, '线上版本已改变'):
            self.send(release, store, notify)
        self.assertNotIn(POINTER, store.writes)

    def test_package_announcement_not_implicitly_published(self):
        files = fixture()
        files['announcement.json'] = announcement_bytes('title', 'notice')
        release, store = self.release(files), Store()
        self.send(release, store)
        self.assertNotIn('announcement.json', store.writes)

    def test_announcement_limits(self):
        with self.assertRaises(ValueError):
            announcement_bytes('title', '')
        with self.assertRaises(ValueError):
            announcement_bytes('title', 'a' * 65536)

    def test_sdk_adapter_constructs_offline_and_put_request(self):
        store = OssStore('openkfo', 'cn-hangzhou', 'test-only', 'test-only')
        requests = []
        class Client:
            def put_object(self, request):
                requests.append(request)
        store.client = Client()
        store.put(POINTER, b'{}')
        self.assertEqual(requests[0].bucket, 'openkfo')
        self.assertEqual(requests[0].body, b'{}')
        self.assertEqual(requests[0].cache_control, 'no-cache')
        self.assertFalse(requests[0].forbid_overwrite)

    def test_real_sdk_stream_reader_and_metadata_limit(self):
        from alibabacloud_oss_v2.io_utils import StreamBodyReader
        store = OssStore('openkfo', 'cn-hangzhou', 'test-only', 'test-only')
        for size in (0, 100, MAX_METADATA, MAX_METADATA + 1):
            data = b'x' * size
            response = MagicMock()
            response.iter_bytes.side_effect = lambda **kw: (data[i:i+kw['block_size']] for i in range(0, len(data), kw['block_size']))
            store.client = Mock()
            store.client.get_object.return_value = SimpleNamespace(body=StreamBodyReader(response))
            if size > MAX_METADATA:
                with self.assertRaisesRegex(ValueError, '过大'):
                    store.get(POINTER)
            else:
                self.assertEqual(store.get(POINTER), data)
            response.__exit__.assert_called_once()

    def test_wrapped_service_errors(self):
        from alibabacloud_oss_v2.exceptions import OperationError, ServiceError
        store = OssStore('openkfo', 'cn-hangzhou', 'test-only', 'test-only')
        for code, status in [('NoSuchKey', 404), ('AccessDenied', 403), ('InvalidAccessKeyId', 403)]:
            cause = ServiceError(status_code=status, code=code, request_id='test-request', message='test', ec='', timestamp='', request_target='test')
            error = OperationError(name='GetObject', error=cause)
            store.client = Mock()
            store.client.get_object.side_effect = error
            for read in (store.get, store.remote_hash):
                if status == 404:
                    self.assertIsNone(read(POINTER))
                else:
                    with self.assertRaises(OperationError):
                        read(POINTER)
            self.assertIn(code, error_message(error))


if __name__ == '__main__':
    unittest.main()
