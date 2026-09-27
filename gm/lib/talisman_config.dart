import 'item_pictures.dart';

import 'package:flutter/material.dart';

class TalismanConfigPage extends StatefulWidget {
  const TalismanConfigPage({
    super.key,
    required this.api,
    required this.environment,
  });
  final Future<dynamic> Function(Map<String, dynamic>) api;
  final String environment;
  @override
  State<TalismanConfigPage> createState() => _TalismanConfigPageState();
}

class _TalismanConfigPageState extends State<TalismanConfigPage> {
  late final pictures = ItemPictures(widget.api);
  List<Map<String, dynamic>> uses = [], repairs = [];
  bool repairing = false;
  List<Map<String, dynamic>> get levels => repairing ? repairs : uses;
  int? revision;
  bool busy = false, enabled = false;
  String status = '';
  @override
  void initState() {
    super.initState();
    load();
  }

  Future<void> run(Future<void> Function() action) async {
    if (busy) return;
    setState(() => busy = true);
    try {
      await action();
    } catch (e) {
      if (mounted) setState(() => status = '操作失败：$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  void apply(dynamic data) {
    if (!mounted) return;
    final rules = data['rules'] as Map;
    List<Map<String, dynamic>> rows(String key) => ((rules[key] as List?) ?? [])
        .map((v) => Map<String, dynamic>.from(v as Map))
        .toList();
    final newUses = rows('uses'), newRepairs = rows('repairs');
    setState(() {
      revision = data['revision'] as int;
      enabled = rules['enabled'] == true;
      uses = newUses;
      repairs = newRepairs;
      status = '已读取 ${widget.environment}，版本 $revision';
    });
  }

  Future<void> load() => run(() async {
    setState(() => revision = null);
    apply(await widget.api({'operation': 'talisman_settings_get'}));
  });
  Future<void> importClientRules() => run(() async {
    final result = await widget.api({'operation': 'talisman_client_rules'});
    if (!mounted) return;
    final existing = uses.map((r) => r['item']).toSet();
    final additions = (result as List)
        .where((r) => !existing.contains(r['item']))
        .map((r) => Map<String, dynamic>.from(r as Map))
        .toList();
    setState(() {
      uses.addAll(additions);
      status = '从客户端补充 ${additions.length} 条宠物/法宝规则，保留已有设置。请核对后保存。';
    });
  });
  Future<void> save() => run(() async {
    if (enabled && uses.isEmpty && repairs.isEmpty) {
      throw const FormatException('启用至少需要一条规则');
    }
    apply(
      await widget.api({
        'operation': 'talisman_settings_save',
        'talisman_settings': {
          'revision': revision,
          'rules': {'enabled': enabled, 'uses': uses, 'repairs': repairs},
        },
      }),
    );
    if (mounted) {
      setState(
        () => status = '已保存到 ${widget.environment}；新版服务器动态读取；修理窗口需重新打开获取报价',
      );
    }
  });
  Future<void> edit(int index) async {
    final row = index < levels.length ? levels[index] : <String, dynamic>{};
    final fields = repairing
        ? {
            'item': '法宝／宠物物品ID',
            'material': '修理材料物品ID',
            'quantity': '材料数量',
            'capacity': '修复后额度原值',
          }
        : {
            'item': '法宝／宠物物品ID',
            'active_cost': '主动消耗原值',
            'passive_cost': '被动消耗原值',
          };
    final values = {for (final key in fields.keys) key: '${row[key] ?? ''}'};
    String error = '';
    final result = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, update) => AlertDialog(
          title: Text(repairing ? '修理规则' : '使用规则'),
          content: SizedBox(
            width: 360,
            child: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  for (final field in fields.entries)
                    TextFormField(
                      key: ValueKey(field.key),
                      initialValue: values[field.key],
                      keyboardType: TextInputType.number,
                      decoration: InputDecoration(labelText: field.value),
                      onChanged: (v) => update(() => values[field.key] = v),
                    ),
                  pictures.byId(int.tryParse(values['item'] ?? '') ?? 0),
                  if (repairing)
                    pictures.byId(int.tryParse(values['material'] ?? '') ?? 0),
                  Text(error),
                ],
              ),
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () {
                final parsed = <String, dynamic>{...row};
                for (final field in fields.keys) {
                  final n = int.tryParse(values[field]!.trim());
                  final max = field == 'item' || field == 'material'
                      ? 4294967295
                      : 65535;
                  if (n == null ||
                      n < 0 ||
                      n > max ||
                      (field != 'active_cost' &&
                          field != 'passive_cost' &&
                          n == 0)) {
                    update(() => error = '${fields[field]}无效');
                    return;
                  }
                  parsed[field] = n;
                }
                if (levels.asMap().entries.any(
                  (e) => e.key != index && e.value['item'] == parsed['item'],
                )) {
                  update(() => error = '同类规则物品ID重复');
                  return;
                }
                if (repairing && parsed['item'] == parsed['material']) {
                  update(() => error = '修理材料不能是法宝自身');
                  return;
                }
                Navigator.pop(context, parsed);
              },
              child: const Text('确定'),
            ),
          ],
        ),
      ),
    );
    if (!mounted || result == null) return;
    setState(() {
      if (index == levels.length) {
        levels.add(result);
      } else {
        levels[index] = result;
      }
      status = '有未保存的修改';
    });
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: Text('宠物／法宝 · ${widget.environment}')),
    body: Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        children: [
          const Text(
            '额度按百分之一填写：100表示1额度，0消耗表示免费。修理扣材料，不扣金币。需部署支持此配置的新版服务器。改价后须重新获取修理报价；已接受的使用事件保留原费用。',
          ),
          SwitchListTile(
            title: const Text('启用宠物／法宝规则'),
            value: enabled,
            onChanged: busy || revision == null
                ? null
                : (v) => setState(() => enabled = v),
          ),
          SegmentedButton<bool>(
            segments: const [
              ButtonSegment(value: false, label: Text('使用消耗')),
              ButtonSegment(value: true, label: Text('材料修理')),
            ],
            selected: {repairing},
            onSelectionChanged: busy
                ? null
                : (v) => setState(() => repairing = v.first),
          ),
          Expanded(
            child: ListView.builder(
              itemCount: levels.length,
              itemBuilder: (context, i) {
                final r = levels[i];
                return ListTile(
                  title: pictures.byId(r['item'] as int),
                  trailing: IconButton(
                    tooltip: '删除规则',
                    icon: const Icon(Icons.delete_outline),
                    onPressed: busy || revision == null
                        ? null
                        : () => setState(() {
                            levels.removeAt(i);
                            status = '有未保存的修改';
                          }),
                  ),
                  subtitle: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      if (repairing) pictures.byId(r['material'] as int),
                      Text(
                        repairing
                            ? '材料 ${r['material']} × ${r['quantity']} · 容量 ${r['capacity']}'
                            : '主动 ${r['active_cost']} · 被动 ${r['passive_cost']}',
                      ),
                    ],
                  ),
                  onTap: busy || revision == null ? null : () => edit(i),
                );
              },
            ),
          ),
          Text(status),
          Wrap(
            spacing: 8,
            children: [
              TextButton(
                onPressed: busy ? null : load,
                child: const Text('重新读取'),
              ),
              TextButton(
                onPressed: busy || revision == null ? null : importClientRules,
                child: const Text('从客户端补齐宠物/法宝规则'),
              ),
              TextButton(
                onPressed: busy || revision == null || levels.length >= 4096
                    ? null
                    : () => edit(levels.length),
                child: const Text('新增规则'),
              ),
              FilledButton(
                onPressed: busy || revision == null ? null : save,
                child: const Text('保存法宝配置'),
              ),
            ],
          ),
        ],
      ),
    ),
  );
}
