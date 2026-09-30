import 'dart:convert';

typedef CatalogApi = Future<dynamic> Function(Map<String, dynamic>);

/// Only client catalogues are cached. Balances, players and write results stay live.
class CatalogCache {
  CatalogCache(this.api, {this.stamp});
  final CatalogApi api;
  final String Function()? stamp;
  final _pending = <String, Future<dynamic>>{};
  String? _stamp;
  int _generation = 0;
  void clear() {
    _pending.clear();
    _generation++;
  }

  Future<dynamic> call(Map<String, dynamic> input) async {
    final request = Map<String, dynamic>.from(input);
    final refresh = request.remove('_refresh') == true;
    final operation = request['operation'] as String? ?? '';
    final current = stamp?.call();
    if (refresh || current != _stamp) {
      clear();
      _stamp = current;
    }
    final read = const {
      'catalog',
      'weapon_catalog',
      'client_directory_get',
      'weapon_combo_chain',
      'weapon_combo_rule',
      'weapon_effect_view',
      'weapon_remap_options',
    }.contains(operation);
    if (!read) {
      final clientRead = const {
        'weapon_combo_chain',
        'weapon_combo_rule',
        'weapon_effect_view',
        'weapon_effects_preview',
        'weapon_merge_export',
        'weapon_merge_packages',
        'weapon_merge_preview',
        'weapon_merge_compare',
        'weapon_merge_stage',
        'weapon_merge_save',
        'weapon_package',
        'weapon_remap_options',
        'weapon_stage_track',
        'weapon_template_resolve',
      }.contains(operation);
      final mutation =
          !clientRead &&
          (operation.startsWith('weapon_') ||
              operation == 'client_directory_set');
      if (mutation) clear();
      try {
        return await api(request);
      } finally {
        if (mutation) clear();
      }
    }
    final key = jsonEncode(request);
    if (!_pending.containsKey(key) && _pending.length >= 64) {
      final oldDetails = _pending.keys.where(
        (k) => (jsonDecode(k) as Map).containsKey('weapon'),
      );
      if (oldDetails.isNotEmpty) _pending.remove(oldDetails.first);
    }
    final generation = _generation;
    final future = _pending.putIfAbsent(key, () => api(request));
    try {
      return await future;
    } catch (_) {
      if (_generation == generation && identical(_pending[key], future)) {
        _pending.remove(key);
      }
      rethrow;
    }
  }
}
