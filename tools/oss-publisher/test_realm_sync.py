import hashlib
import json
import tempfile
import threading
import unittest
from pathlib import Path
from unittest.mock import Mock

from publisher import publish, POINTER
from realm_sync import RealmSync, FIXED, BASE
import test_publisher as fixtures


class SyncTests(unittest.TestCase):
    def test_preflight_failure_does_not_publish_pointer(self):
        helper = fixtures.PublisherTests(); helper.setUp()
        self.addCleanup(helper.doCleanups)
        release, store = helper.release(), fixtures.Store()
        before = store.get(POINTER)
        def reject(*args):
            raise ValueError('server configuration mismatch')
        with self.assertRaisesRegex(ValueError, 'mismatch'):
            publish(release, store, lambda *args: None, threading.Event(), helper.root/'backups', before_commit=reject)
        self.assertEqual(before, store.get(POINTER))
        self.assertNotIn(POINTER, store.writes)

    def test_concurrent_pointer_change_after_preflight_is_rejected(self):
        helper = fixtures.PublisherTests(); helper.setUp(); self.addCleanup(helper.doCleanups)
        release, store = helper.release(), fixtures.Store()
        def change(*args): store.files[POINTER] = b'{"version":"other"}'
        with self.assertRaisesRegex(ValueError, '线上版本已改变'):
            publish(release, store, lambda *args: None, threading.Event(), helper.root/'backups', before_commit=change)
        self.assertEqual(store.files[POINTER], b'{"version":"other"}')

    def test_activation_failure_reports_partial_success(self):
        sync = RealmSync('unused', Mock())
        sync.request = {'version':'test','manifest_hash':'a'*64}
        sync.call = Mock(side_effect=RuntimeError('offline'))
        with self.assertRaisesRegex(RuntimeError, 'OSS 已发布，但二区同步未完成'):
            sync.activate()

    def test_bridge_hash_mismatch_blocks_before_server_call(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); folder=root/'manifest/test';folder.mkdir(parents=True)
            (folder/'client.json').write_text(json.dumps({'files':[{'path':'Data/config.spf2','sha256':'a'*64}]}))
            bridge=json.dumps({'config_hash':'b'*64}).encode()
            (folder/'launcher.json').write_text(json.dumps({'files':[{'path':'launcher-files/bridge.json','url':BASE+'bridge','sha256':hashlib.sha256(bridge).hexdigest()}]}))
            release=Mock(root=root,version='test');store=Mock();store.get.return_value=bridge
            sync=RealmSync('unused',Mock());sync.call=Mock()
            with self.assertRaisesRegex(ValueError,'哈希不同'):sync.prepare(release,store)
            sync.call.assert_not_called()

    def test_local_credentials_are_encrypted(self):
        import os
        if os.name != 'nt':self.skipTest('Windows DPAPI')
        import credentials
        with tempfile.TemporaryDirectory() as tmp:
            folder=Path(tmp);data={'secret':'self-test-secret'}
            credentials.save(folder,data)
            self.assertEqual(credentials.load(folder),data)
            self.assertNotIn(b'self-test-secret',(folder/'credentials.dpapi').read_bytes())

    def test_fixed_oss_destination(self):
        self.assertEqual(FIXED,{'bucket':'openkfo','region':'cn-hangzhou','base':BASE})

if __name__=='__main__':unittest.main()
