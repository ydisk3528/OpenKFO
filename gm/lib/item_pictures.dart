import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';

class ItemPictures {
  ItemPictures(this.api);
  final Future<dynamic> Function(Map<String, dynamic>) api;
  Future<dynamic>? _catalog;
  Widget byId(int id) {
    if (id == 0) return const SizedBox.shrink();
    return FutureBuilder<dynamic>(
      future: _catalog ??= api({'operation': 'catalog'}),
      builder: (context, snapshot) {
        final rows = snapshot.data?['items'] as List? ?? [];
        final matches = rows.where((i) => i['id'] == id).toList();
        final item = matches.length == 1
            ? Map<String, dynamic>.from(matches.first)
            : null;
        return Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            preview(item),
            const SizedBox(width: 8),
            Flexible(child: Text('${item?['name'] ?? '未匹配道具'} · $id')),
          ],
        );
      },
    );
  }

  final _pending = <String, Completer<Uint8List?>>{};
  bool _scheduled = false;
  Future<void> _loadPending() async {
    while (_pending.isNotEmpty) {
      final keys = _pending.keys.take(24).toList();
      final batch = {for (final key in keys) key: _pending.remove(key)!};
      try {
        final result = await api({'operation': 'shop_images', 'keys': keys});
        for (final entry in batch.entries) {
          Uint8List? bytes;
          try {
            if (result is Map && result[entry.key] is String)
              bytes = base64Decode(result[entry.key]);
          } catch (_) {
            /* Invalid individual images must not block the other items. */
          }
          entry.value.complete(bytes);
        }
      } catch (_) {
        for (final pending in batch.values) {
          pending.complete(null);
        }
      }
    }
    _scheduled = false;
  }

  final _images = <String, Future<Uint8List?>>{};
  Widget preview(Map<String, dynamic>? item, {double size = 48}) {
    final key = item?['key'];
    if (key is! String)
      return SizedBox(
        width: size,
        height: size,
        child: Icon(Icons.image_not_supported_outlined),
      );
    if (_images.length >= 256 && !_images.containsKey(key))
      _images.remove(_images.keys.first);
    final image = _images.putIfAbsent(key, () {
      final pending = Completer<Uint8List?>();
      _pending[key] = pending;
      if (!_scheduled) {
        _scheduled = true;
        scheduleMicrotask(_loadPending);
      }
      return pending.future;
    });
    return SizedBox(
      width: size,
      height: size,
      child: FutureBuilder<Uint8List?>(
        future: image,
        builder: (context, snapshot) {
          if (snapshot.connectionState != ConnectionState.done)
            return const Center(
              child: CircularProgressIndicator(strokeWidth: 2),
            );
          final bytes = snapshot.data;
          if (bytes == null)
            return const Tooltip(
              message: '暂无可用图片',
              child: Icon(Icons.image_not_supported_outlined),
            );
          return InkWell(
            onTap: () => showDialog<void>(
              context: context,
              builder: (c) => AlertDialog(
                title: Text('${item?['name'] ?? '奖励预览'}'),
                content: SizedBox(
                  width: 280,
                  height: 280,
                  child: Image.memory(
                    bytes,
                    fit: BoxFit.contain,
                    errorBuilder: (_, e, s) =>
                        const Icon(Icons.broken_image_outlined),
                  ),
                ),
                actions: [
                  TextButton(
                    onPressed: () => Navigator.pop(c),
                    child: const Text('关闭'),
                  ),
                ],
              ),
            ),
            child: Image.memory(
              bytes,
              fit: BoxFit.contain,
              errorBuilder: (_, e, s) =>
                  const Icon(Icons.broken_image_outlined),
            ),
          );
        },
      ),
    );
  }
}
