import 'item_pictures.dart';

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';

typedef PickerApi = Future<dynamic> Function(Map<String, dynamic>);

class RewardCatalog {
  RewardCatalog(this.api, this.options);
  final PickerApi api;
  final List<Map<String, dynamic>> options;
  static Future<RewardCatalog> load(PickerApi api) async {
    final results = await Future.wait([
      api({'operation': 'catalog'}),
      api({'operation': 'definitions_get'}),
    ]);
    final names = {
      for (final i in results[0]['items'] as List)
        '${i['kind']}:${i['id']}': Map<String, dynamic>.from(i),
    };
    final result = <Map<String, dynamic>>[];
    final present = <String>{};
    for (final row in results[1] as List) {
      final b = base64Decode(row['record'] as String);
      if (b.length != 68) continue;
      final id = ByteData.sublistView(b).getUint32(5, Endian.little);
      final k = '${b[4]}:$id';
      final item = names[k];
      if (item == null) continue;
      present.add(k);
      result.add({
        ...item,
        'definition': row['key'],
        'days': row['days'],
        'label':
            '${item['name']} · ${item['category']} · ${row['days'] == 0 ? '无服务器到期' : '${row['days']}天'}',
      });
    }
    for (final e in names.entries) {
      if (!present.contains(e.key))
        result.add({
          ...e.value,
          'definition': null,
          'label': '${e.value['name']} · ${e.value['category']} · 新奖励规格',
        });
    }
    return RewardCatalog(api, result);
  }

  late final pictures = ItemPictures(api);
  Widget preview(Map<String, dynamic>? item) => pictures.preview(item);

  Widget definitionPreview(int key) {
    for (final item in options) {
      if (item['definition'] == key) return preview(item);
    }
    return preview(null);
  }

  String name(int key) {
    for (final o in options) {
      if (o['definition'] == key) return o['label'] as String;
    }
    return '旧配置奖励（当前目录未匹配，请重新选择）';
  }

  Future<int?> pick(BuildContext context, {bool weaponsOnly = false}) async {
    String filter = '';
    bool busy = false;
    String error = '';
    return showDialog<int>(
      context: context,
      builder: (c) => StatefulBuilder(
        builder: (c, refresh) {
          final visible = options
              .where(
                (o) =>
                    (!weaponsOnly || o['kind'] == 25) &&
                    '${o['name']} ${o['category']}'.contains(filter),
              )
              .toList();
          return AlertDialog(
            title: const Text('按名称选择奖励'),
            content: SizedBox(
              width: 660,
              height: 480,
              child: Column(
                children: [
                  TextField(
                    autofocus: true,
                    decoration: const InputDecoration(
                      labelText: '搜索武器、道具、称号名称',
                    ),
                    onChanged: (v) => refresh(() => filter = v.trim()),
                  ),
                  const Text('选择已有发放规格；新规格为1份，限时物品7天。称号指背包中的装饰称号。'),
                  if (busy) const LinearProgressIndicator(),
                  Text(error, style: const TextStyle(color: Colors.red)),
                  Expanded(
                    child: ListView.builder(
                      itemCount: visible.length,
                      itemBuilder: (_, i) {
                        final o = visible[i];
                        return ListTile(
                          leading: preview(o),
                          title: Text(o['name'] as String),
                          subtitle: Text(o['label'] as String),
                          onTap: busy
                              ? null
                              : () async {
                                  refresh(() => busy = true);
                                  try {
                                    int? key = o['definition'] as int?;
                                    if (key == null) {
                                      final r = await api({
                                        'operation': 'definition_from_item',
                                        'key': o['key'],
                                      });
                                      key = r['key'] as int;
                                      o['definition'] = key;
                                    }
                                    if (c.mounted) Navigator.pop(c, key);
                                  } catch (e) {
                                    if (c.mounted)
                                      refresh(() {
                                        busy = false;
                                        error = '$e';
                                      });
                                  }
                                },
                        );
                      },
                    ),
                  ),
                ],
              ),
            ),
            actions: [
              TextButton(
                onPressed: busy ? null : () => Navigator.pop(c),
                child: const Text('取消'),
              ),
            ],
          );
        },
      ),
    );
  }
}

class RewardItemsField extends StatelessWidget {
  const RewardItemsField({
    super.key,
    required this.catalog,
    required this.items,
    required this.onChanged,
    this.maximum = 8,
    this.weaponsOnly = false,
  });
  final Future<RewardCatalog> catalog;
  final List<int> items;
  final ValueChanged<List<int>> onChanged;
  final int maximum;
  final bool weaponsOnly;
  @override
  Widget build(BuildContext context) => FutureBuilder<RewardCatalog>(
    future: catalog,
    builder: (context, s) {
      if (s.hasError) return Text('读取物品名称失败：${s.error}');
      if (!s.hasData) return const LinearProgressIndicator();
      final data = s.data!;
      return Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          for (var i = 0; i < items.length; i++)
            ListTile(
              leading: data.definitionPreview(items[i]),
              dense: true,
              title: Text(data.name(items[i])),
              trailing: IconButton(
                tooltip: '移除奖励',
                icon: const Icon(Icons.close),
                onPressed: () {
                  final next = List<int>.from(items)..removeAt(i);
                  onChanged(next);
                },
              ),
            ),
          OutlinedButton(
            onPressed: items.length >= maximum
                ? null
                : () async {
                    final key = await data.pick(
                      context,
                      weaponsOnly: weaponsOnly,
                    );
                    if (key != null) onChanged([...items, key]);
                  },
            child: const Text('选择武器 / 道具 / 称号'),
          ),
        ],
      );
    },
  );
}

class RewardPreviews extends StatelessWidget {
  const RewardPreviews({super.key, required this.catalog, required this.ids});
  final Future<RewardCatalog> catalog;
  final List<int> ids;
  @override
  Widget build(BuildContext context) {
    return FutureBuilder<RewardCatalog>(
      future: catalog,
      builder: (context, snapshot) {
        if (ids.where((id) => id > 0).isEmpty) return const SizedBox.shrink();
        if (snapshot.hasError) return const Text('道具图片读取失败');
        if (!snapshot.hasData) return const LinearProgressIndicator();
        final data = snapshot.data!;
        return Wrap(
          spacing: 12,
          runSpacing: 8,
          children: [
            for (final id in ids.where((id) => id > 0))
              SizedBox(
                width: 240,
                child: Row(
                  children: [
                    data.definitionPreview(id),
                    const SizedBox(width: 6),
                    Expanded(child: Text(data.name(id))),
                  ],
                ),
              ),
          ],
        );
      },
    );
  }
}
