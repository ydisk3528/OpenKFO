import 'reward_picker.dart';

import 'package:flutter/material.dart';

class ItemDefinitionsPage extends StatefulWidget {
  const ItemDefinitionsPage({
    super.key,
    required this.api,
    required this.environment,
  });
  final Future<dynamic> Function(Map<String, dynamic>) api;
  final String environment;
  @override
  State<ItemDefinitionsPage> createState() => _ItemDefinitionsPageState();
}

class _ItemDefinitionsPageState extends State<ItemDefinitionsPage> {
  List<Map<String, dynamic>> rows = [];
  bool busy = false;
  String status = '', filter = '';
  RewardCatalog? catalog;
  @override
  void initState() {
    super.initState();
    load();
  }

  Future<void> load() async {
    setState(() => busy = true);
    try {
      final result = await widget.api({'operation': 'definitions_get'});
      final names = await RewardCatalog.load(widget.api);
      if (mounted) {
        setState(() {
          catalog = names;
          rows = (result as List)
              .map((e) => Map<String, dynamic>.from(e))
              .toList();
        });
      }
    } catch (e) {
      if (mounted) setState(() => status = '$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> edit(Map<String, dynamic> row) async {
    final days = TextEditingController(text: '${row['days']}');
    String error = '';
    final result = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (c) => StatefulBuilder(
        builder: (c, refresh) => AlertDialog(
          title: const Text('修改奖励物品期限'),
          content: SizedBox(
            width: 420,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(catalog?.name(row['key'] as int) ?? '奖励物品'),
                TextField(
                  controller: days,
                  keyboardType: TextInputType.number,
                  decoration: const InputDecoration(
                    labelText: '服务器到期天数（0=不设到期）',
                  ),
                ),
                Text(error, style: const TextStyle(color: Colors.red)),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(c),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () {
                final d = int.tryParse(days.text);
                if (d == null || d < 0 || d > 3650) {
                  refresh(() => error = '天数须为0–3650');
                  return;
                }
                Navigator.pop(c, {...row, 'days': d});
              },
              child: const Text('保存'),
            ),
          ],
        ),
      ),
    );
    await Future<void>.delayed(const Duration(milliseconds: 300));
    days.dispose();
    if (result == null || !mounted) return;
    setState(() => busy = true);
    try {
      await widget.api({'operation': 'definition_save', 'definition': result});
      if (mounted) setState(() => status = '已保存到${widget.environment}');
    } catch (e) {
      if (mounted) setState(() => status = '$e');
    }
    if (mounted) await load();
  }

  @override
  Widget build(BuildContext context) {
    final visible = rows
        .where((r) => (catalog?.name(r['key'] as int) ?? '').contains(filter))
        .toList();
    return Scaffold(
      appBar: AppBar(title: Text('${widget.environment} · 奖励物品定义')),
      body: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          children: [
            const Text('按客户端名称管理发放规格。商品下架或改价不影响奖励；修改期限只影响今后的发放。'),
            TextField(
              decoration: const InputDecoration(labelText: '搜索武器 / 道具 / 称号名称'),
              onChanged: (v) => setState(() => filter = v.trim()),
            ),
            if (busy) const LinearProgressIndicator(),
            SelectableText(status),
            Expanded(
              child: ListView.builder(
                itemCount: visible.length,
                itemBuilder: (_, i) {
                  final r = visible[i];
                  return ListTile(
                    leading: catalog?.definitionPreview(r['key'] as int),
                    title: SelectableText(
                      catalog?.name(r['key'] as int) ?? '读取名称中',
                    ),
                    subtitle: Text(
                      '服务器期限 ${r['days']} 天 · 版本 ${r['revision']}',
                    ),
                    trailing: Wrap(
                      children: [
                        TextButton(
                          onPressed: busy ? null : () => edit(r),
                          child: const Text('修改期限'),
                        ),
                      ],
                    ),
                  );
                },
              ),
            ),
            OutlinedButton(
              onPressed: busy || catalog == null
                  ? null
                  : () async {
                      final key = await catalog!.pick(context);
                      if (key != null && mounted) await load();
                    },
              child: const Text('按名称添加奖励物品'),
            ),
            OutlinedButton(
              onPressed: busy ? null : load,
              child: const Text('重新读取'),
            ),
          ],
        ),
      ),
    );
  }
}
