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
      final listRequest = {'operation': 'weapon_list', 'environment': 'local'};
      final detailRequest = {
        'operation': 'weapon_detail',
        'environment': 'local',
        'weapon': 253013,
      };
      final a = cache.call(listRequest), b = cache.call(listRequest);
      pending.complete({'read': 1});
      expect(await a, await b);
      expect(calls, ['local:weapon_list']);
      expect(await cache.call(listRequest), {'read': 1});
      expect(calls, ['local:weapon_list']);
      final detailA = cache.call(detailRequest),
          detailB = cache.call(detailRequest);
      expect(await detailA, await detailB);
      expect(calls, ['local:weapon_list', 'local:weapon_detail']);
      expect(await cache.call(detailRequest), {'read': 2});
      expect(calls, ['local:weapon_list', 'local:weapon_detail']);
      await cache.call({'operation': 'wallet_accounts'});
      expect(await cache.call(listRequest), {'read': 1});
      expect(calls, [
        'local:weapon_list',
        'local:weapon_detail',
        'null:wallet_accounts',
      ]);
      await cache.call({...detailRequest, 'weapon': 253014});
      expect(calls, [
        'local:weapon_list',
        'local:weapon_detail',
        'null:wallet_accounts',
        'local:weapon_detail',
      ]);
      stamp = 'v2';
      await cache.call(listRequest);
      expect(calls.length, 5);
      await cache.call({'operation': 'weapon_variant_set'});
      await cache.call(detailRequest);
      expect(calls.length, 7);
      await cache.call({...listRequest, '_refresh': true});
      expect(calls.length, 8);
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
