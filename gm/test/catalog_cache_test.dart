import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/catalog_cache.dart';

void main() {
  test(
    'catalog cache shares pending reads and invalidates only client data',
    () async {
      var stamp = 'v1';
      final calls = <String>[];
      final pending = Completer<dynamic>();
      final cache = CatalogCache((r) async {
        expect(r.containsKey('_refresh'), false);
        calls.add('${r['environment']}:${r['operation']}');
        if (calls.length == 1) return pending.future;
        return {'read': calls.length};
      }, stamp: () => stamp);
      final request = {'operation': 'weapon_catalog', 'environment': 'local'};
      final a = cache.call(request), b = cache.call(request);
      pending.complete({'read': 1});
      expect(await a, await b);
      expect(calls.length, 1);
      await cache.call({'operation': 'wallet_accounts'});
      await cache.call(request);
      expect(calls.length, 2);
      await cache.call({...request, 'environment': 'online'});
      expect(calls.length, 3);
      stamp = 'v2';
      await cache.call(request);
      expect(calls.length, 4);
      await cache.call({'operation': 'weapon_apply'});
      await cache.call(request);
      expect(calls.length, 6);
      await cache.call({...request, '_refresh': true});
      expect(calls.length, 7);
    },
  );
  test('failed reads can be retried', () async {
    var attempts = 0;
    final cache = CatalogCache((r) async {
      if (++attempts == 1) throw StateError('failed');
      return 42;
    });
    await expectLater(cache.call({'operation': 'catalog'}), throwsStateError);
    expect(await cache.call({'operation': 'catalog'}), 42);
  });
}
