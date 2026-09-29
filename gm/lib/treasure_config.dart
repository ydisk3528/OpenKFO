import 'dart:convert';

import 'package:flutter/material.dart';

import 'reward_picker.dart';

class TreasureConfigPage extends StatefulWidget {
  const TreasureConfigPage({
    super.key,
    required this.api,
    required this.environment,
  });
  final PickerApi api;
  final String environment;
  @override
  State<TreasureConfigPage> createState() => _TreasureConfigPageState();
}

class _TreasureConfigPageState extends State<TreasureConfigPage> {
  final form = GlobalKey<FormState>();
  late final catalog = RewardCatalog.load(widget.api);
  List<Map<String, dynamic>> pools = [];
  int? revision;
  bool busy = false;
  String status = '';
  final previews = <int, List<dynamic>>{};
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
    final next = data['revision'];
    if (next is! int || data['pools'] is! List) {
      throw const FormatException('百宝配置格式无效');
    }
    final rows = (jsonDecode(jsonEncode(data['pools'])) as List)
        .map((e) => Map<String, dynamic>.from(e))
        .toList();
    if (!mounted) return;
    setState(() {
      revision = next;
      pools = rows;
      previews.clear();
      status = '已读取配置，版本 $next';
    });
  }

  Future<void> load() => run(() async {
    setState(() => revision = null);
    apply(await widget.api({'operation': 'treasure_get'}));
  });
  Future<void> save() => run(() async {
    if (revision == null || !form.currentState!.validate()) return;
    for (final p in pools) {
      for (final r in p['prizes'] as List) {
        if ((r['items'] as List).isEmpty &&
            r['gold'] == 0 &&
            r['tickets'] == 0) {
          throw const FormatException('请为每条奖励选择道具或填写金币／点券');
        }
      }
    }
    apply(
      await widget.api({
        'operation': 'treasure_save',
        'treasure': {'revision': revision, 'pools': pools},
      }),
    );
    if (mounted) setState(() => status = '已保存奖池配置；游戏抽奖接入尚待验证');
  });
  Future<void> preview(Map<String, dynamic> pool) => run(() async {
    if (!form.currentState!.validate()) return;
    final rows = await widget.api({
      'operation': 'treasure_preview',
      'treasure': {
        'revision': revision,
        'pools': [pool],
      },
    });
    if (mounted)
      setState(() {
        previews[pool['ticket_kind'] as int] = rows as List;
        status = '六格模拟预览，不扣券、不发奖；修改配置后需重新刷新预览';
      });
  });
  Widget groupPreview(List<dynamic> rows) => FutureBuilder<RewardCatalog>(
    future: catalog,
    builder: (context, snapshot) {
      final total = rows.fold<int>(
        0,
        (sum, row) => sum + (row['weight'] as int),
      );
      return Column(
        children: [
          for (var i = 0; i < rows.length; i++)
            Card(
              child: Padding(
                padding: const EdgeInsets.all(10),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      '第${i + 1}格 · 本组中奖率 ${(100 * (rows[i]['weight'] as int) / total).toStringAsFixed(2)}% · 权重 ${rows[i]['weight']}',
                    ),
                    if ((rows[i]['gold'] as int) > 0)
                      Text('金币 ${rows[i]['gold']}'),
                    if ((rows[i]['tickets'] as int) > 0)
                      Text('点券 ${rows[i]['tickets']}'),
                    for (final key in (rows[i]['items'] as List? ?? []))
                      ListTile(
                        leading: snapshot.data?.definitionPreview(key as int),
                        title: Text(
                          snapshot.data?.name(key as int) ?? '正在读取道具…',
                        ),
                      ),
                  ],
                ),
              ),
            ),
        ],
      );
    },
  );
  Widget number(
    Map row,
    String key,
    String label,
    int min,
    int max, {
    VoidCallback? changed,
  }) => SizedBox(
    width: 155,
    child: TextFormField(
      key: ValueKey('${identityHashCode(row)}-$key'),
      initialValue: '${row[key] ?? 0}',
      decoration: InputDecoration(labelText: label),
      keyboardType: TextInputType.number,
      validator: (v) {
        final n = int.tryParse(v ?? '');
        return n == null || n < min || n > max ? '范围 $min–$max' : null;
      },
      onChanged: (v) {
        row[key] = int.tryParse(v) ?? -1;
        changed?.call();
      },
    ),
  );
  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: Text('百宝奖池 · ${widget.environment}')),
    body: Column(
      children: [
        if (busy) const LinearProgressIndicator(),
        const Padding(
          padding: EdgeInsets.all(12),
          child: Text(
            '总奖池刷新时随机选出6条不同奖励；中奖只在当前6格内按权重计算。权重越高越容易中奖，不影响刷新出现机会。游戏协议尚未接通，当前仅配置和模拟预览，不扣券、不发奖。',
          ),
        ),
        Expanded(
          child: AbsorbPointer(
            absorbing: busy || revision == null,
            child: Form(
              key: form,
              child: ListView(
                padding: const EdgeInsets.all(16),
                children: [
                  for (final p in pools)
                    Card(
                      child: ExpansionTile(
                        key: ObjectKey(p),
                        initiallyExpanded: true,
                        title: Text('${p['name']}'),
                        childrenPadding: const EdgeInsets.all(12),
                        children: [
                          number(p, 'cost', '每次抽奖消耗券数', 1, 999),
                          const Text(
                            '至少配置6条；超过6条才会换出不同组合。单格中奖率 = 该格权重 ÷ 当前6格权重之和。',
                          ),
                          TextButton.icon(
                            onPressed: () => preview(p),
                            icon: const Icon(Icons.refresh),
                            label: const Text('刷新六格预览'),
                          ),
                          if (previews[p['ticket_kind']] != null)
                            groupPreview(previews[p['ticket_kind']]!),
                          for (final dynamic value in p['prizes'] as List)
                            Builder(
                              builder: (context) {
                                final row = value as Map;
                                return Card(
                                  key: ObjectKey(row),
                                  child: Padding(
                                    padding: const EdgeInsets.all(12),
                                    child: Column(
                                      crossAxisAlignment:
                                          CrossAxisAlignment.start,
                                      children: [
                                        Row(
                                          children: [
                                            Expanded(
                                              child: Text(
                                                '总奖池奖励 ${(p['prizes'] as List).indexOf(row) + 1}',
                                              ),
                                            ),
                                            IconButton(
                                              tooltip: '删除奖励',
                                              onPressed: () => setState(
                                                () => (p['prizes'] as List)
                                                    .remove(row),
                                              ),
                                              icon: const Icon(
                                                Icons.delete_outline,
                                              ),
                                            ),
                                          ],
                                        ),
                                        Wrap(
                                          spacing: 12,
                                          runSpacing: 8,
                                          children: [
                                            number(
                                              row,
                                              'weight',
                                              '中奖权重',
                                              1,
                                              1000000,
                                              changed: () => setState(() {}),
                                            ),
                                            number(
                                              row,
                                              'gold',
                                              '金币',
                                              0,
                                              1000000,
                                            ),
                                            number(
                                              row,
                                              'tickets',
                                              '点券',
                                              0,
                                              1000000,
                                            ),
                                          ],
                                        ),
                                        RewardItemsField(
                                          catalog: catalog,
                                          items: (row['items'] as List)
                                              .cast<int>(),
                                          onChanged: (v) =>
                                              setState(() => row['items'] = v),
                                        ),
                                      ],
                                    ),
                                  ),
                                );
                              },
                            ),
                          TextButton.icon(
                            onPressed: (p['prizes'] as List).length >= 500
                                ? null
                                : () => setState(
                                    () => (p['prizes'] as List).add({
                                      'weight': 1,
                                      'gold': 0,
                                      'tickets': 0,
                                      'items': <int>[],
                                    }),
                                  ),
                            icon: const Icon(Icons.add),
                            label: const Text('添加奖励'),
                          ),
                        ],
                      ),
                    ),
                ],
              ),
            ),
          ),
        ),
        Padding(
          padding: const EdgeInsets.all(12),
          child: Column(
            children: [
              Text(status),
              Wrap(
                spacing: 12,
                children: [
                  TextButton(
                    onPressed: busy ? null : load,
                    child: const Text('重新读取'),
                  ),
                  FilledButton(
                    onPressed: busy || revision == null ? null : save,
                    child: const Text('保存百宝配置'),
                  ),
                ],
              ),
            ],
          ),
        ),
      ],
    ),
  );
}
