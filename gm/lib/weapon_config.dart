import 'item_pictures.dart';


import 'package:flutter/material.dart';

class WeaponConfigPage extends StatefulWidget {
  const WeaponConfigPage({super.key, required this.api});
  final Future<dynamic> Function(Map<String, dynamic>) api;
  @override
  State<WeaponConfigPage> createState() => _WeaponConfigPageState();
}

class _WeaponConfigPageState extends State<WeaponConfigPage> {
  Map<String, dynamic>? data, weapon;
  final form = GlobalKey<FormState>();
  List<Map<String, dynamic>> rules = [];
  String query = '', message = '';
  String weaponType = '全部类型';
  bool busy = true, dirty = false, failed = false;
  String? selectedAction;
  int editorVersion = 0;

  @override
  void initState() {
    super.initState();
    load();
  }

  Future<void> load() async {
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({'operation': 'weapon_catalog'}),
      );
      if (!mounted) return;
      setState(() {
        data = result;
        final weapons = (data!['weapons'] as List);
        final selected = weapons.where(
          (w) => w['id'] == (weapon?['id'] ?? 253013),
        );
        if (selected.isNotEmpty) {
          select(Map<String, dynamic>.from(selected.first));
        } else if (weapons.isNotEmpty) {
          select(Map<String, dynamic>.from(weapons.first));
        }
        busy = false;
      });
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  void select(Map<String, dynamic> value) {
    editorVersion++;
    selectedAction = null;
    weapon = value;
    final stored = data!['drafts']['${value['id']}'] as List? ?? [];
    rules = (value['stages'] as List).map((stage) {
      final saved = stored.where((r) => r['stage'] == stage['stage']);
      return saved.isEmpty
          ? <String, dynamic>{
              'stage': stage['stage'],
              'buff': 0,
              'level': 1,
              'duration': 3000,
            }
          : Map<String, dynamic>.from(saved.first);
    }).toList();
    dirty = false;
  }

  Future<bool> discard() async =>
      !dirty ||
      await showDialog<bool>(
            context: context,
            builder: (context) => AlertDialog(
              title: const Text('有未保存的修改'),
              content: const Text('离开后会丢弃当前修改。'),
              actions: [
                TextButton(
                  onPressed: () => Navigator.pop(context, false),
                  child: const Text('继续编辑'),
                ),
                FilledButton(
                  onPressed: () => Navigator.pop(context, true),
                  child: const Text('丢弃修改'),
                ),
              ],
            ),
          ) ==
          true;

  Future<void> repairEffects() async {
    final selected = weapon;
    if (selected == null || busy) return;
    setState(() { busy = true; message = ''; });
    try {
      final preview = Map<String, dynamic>.from(await widget.api({
        'operation': 'weapon_effects_preview', 'weapon': selected['id'],
      }));
      if (!mounted) return;
      final additions = preview['additions'] as List;
      final issues = preview['issues'] as List;
      final confirm = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: Text('${selected['name']} · 攻击特效'),
          content: SizedBox(width: 560, child: SingleChildScrollView(child: Text([
            '读取：${preview['path']}',
            additions.isEmpty ? '没有可自动补齐的特效。' : '可补齐 ${additions.length} 条：',
            ...additions.map((e) => '${e['id']} → ${e['file']}'),
            if (issues.isNotEmpty) '\n以下项目需要手动处理：',
            ...issues.map((e) => '$e'),
            '\n仅补充特效加载登记，不修改招式、伤害和 BUFF。写入前自动备份，重启游戏后生效；不会自动发布到线上。',
          ].join('\n')))),
          actions: [
            TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('关闭')),
            if (additions.isNotEmpty)
              FilledButton(onPressed: () => Navigator.pop(context, true), child: const Text('备份并补齐')),
          ],
        ),
      );
      if (confirm != true || !mounted) return;
      final result = await widget.api({
        'operation': 'weapon_effects_apply', 'weapon': selected['id'],
        'revision': preview['revision'],
      });
      await load();
      if (mounted) setState(() => message = '${result['message']}');
    } catch (e) {
      if (mounted) setState(() => message = '$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> execute(String operation) async {
    if (!(form.currentState?.validate() ?? false)) return;
    if (operation != 'weapon_save') {
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: Text(operation == 'weapon_apply' ? '应用到本地客户端' : '恢复原效果'),
          content: Text(
            operation == 'weapon_apply'
                ? '将应用「${weapon!['name']}」当前配置的招式伤害、BUFF 和受击效果。修改对使用此客户端的角色生效，不限当前账号。\n\n请先退出游戏；写入前自动备份。重启后加载，实战效果尚待验证。'
                : '恢复「${weapon!['name']}」的原始招式伤害、BUFF 和受击效果，其他武器配置保留。请先退出游戏。',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('确认写入'),
            ),
          ],
        ),
      );
      if (confirmed != true || !mounted) return;
    }
    setState(() {
      busy = true;
      failed = false;
      message = '';
    });
    try {
      final result = await widget.api({
        'operation': operation,
        'weapon': weapon!['id'],
        'rules': rules,
        'revision': data!['revision'],
      });
      if (!mounted) return;
      setState(() {
        dirty = false;
        message = result['message'];
      });
      await load();
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  Future<void> publish() async {
    if (!(form.currentState?.validate() ?? false)) return;
    var notes = '${weapon!['name']}：';
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('更新到线上'),
        content: SizedBox(
          width: 560,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Text(
                '发布全部已保存的武器方案及当前编辑内容。将重启线上服务器，在线玩家会断开；玩家下次启动游戏时下载更新。不会覆盖本地客户端。',
              ),
              const SizedBox(height: 14),
              TextFormField(
                initialValue: notes,
                onChanged: (value) => notes = value,
                minLines: 4,
                maxLines: 8,
                maxLength: 2000,
                decoration: const InputDecoration(
                  labelText: '给玩家看的更新说明（武器名称、改动内容）',
                ),
              ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () {
              if (notes.trim().isNotEmpty) Navigator.pop(context, true);
            },
            child: const Text('发布到线上'),
          ),
        ],
      ),
    );
    final text = notes.trim();
    if (confirmed != true || !mounted) return;
    setState(() {
      busy = true;
      failed = false;
      message = '正在生成、上传并启用线上配置，请勿重复发布…';
    });
    try {
      final result = await widget.api({
        'operation': 'weapon_publish',
        'environment': 'online',
        'weapon': weapon!['id'],
        'rules': rules,
        'revision': data!['revision'],
        'notes': text,
      });
      if (!mounted) return;
      setState(() {
        message = result['message'];
        dirty = false;
      });
      await load();
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  String buffName(dynamic id) {
    final value = int.tryParse('$id') ?? 0;
    if (value == 0) return '无';
    for (final b in (data?['buffs'] as List? ?? [])) {
      if (b['id'] == value) return '${b['name']}';
    }
    return '异常状态 $value（名称未收录）';
  }

  String reactionChoice(dynamic hit, Map<String, dynamic> rule) {
    final original = Map<String, dynamic>.from(hit['values']);
    final current = {...original, ...?rule['properties']?[hit['id']] as Map?};
    final fields = (data?['fields'] as List? ?? []).where(
      (f) => f['key'] != 'SkillDamage' && f['key'] != 'SkillEnhanceDamage',
    );
    if (fields.every(
      (f) => '${current[f['key']]}' == '${original[f['key']]}',
    )) {
      return 'original';
    }
    for (final e in (data?['effects'] as List? ?? [])) {
      if ((e['values'] as Map).entries.every(
        (v) => '${current[v.key]}' == '${v.value}',
      )) {
        return e['id'];
      }
    }
    return 'custom';
  }

  String originalReaction(dynamic hit) {
    final v = hit['values'];
    if ('${v['TripTarget']}' == '1') return '击倒';
    if ('${v['TargetFlurr']}' == '1' && '${v['StandHurtFly']}' == '11') {
      return '上升 / 悬浮';
    }
    if ('${v['RepulseTarget']}' == '1') return '击退';
    return '原受击动作';
  }

  String originalDebuff(dynamic stage) {
    final names = <String>{
      for (final h in (stage['hits'] as List? ?? [])) buffName(h['buff']),
    };
    return names.isEmpty ? '无' : names.join(' / ');
  }

  Widget numberEditors(
    dynamic hit,
    Map<String, dynamic> rule,
    bool enabled, {
    required bool damage,
  }) => Padding(
    padding: const EdgeInsets.all(8),
    child: Wrap(
      spacing: 12,
      runSpacing: 12,
      children: [
        for (final field in (data?['fields'] as List? ?? []).where(
          (f) =>
              damage ==
              ['SkillDamage', 'SkillEnhanceDamage'].contains(f['key']),
        ))
          SizedBox(
            width: 170,
            child: TextFormField(
              key: ValueKey(
                '$editorVersion-${rule['stage']}-${hit['id']}-${field['key']}',
              ),
              initialValue:
                  '${rule['properties']?[hit['id']]?[field['key']] ?? hit['values'][field['key']]}',
              enabled: enabled,
              decoration: InputDecoration(
                labelText: field['name'],
                helperText: '默认 ${hit['values'][field['key']]}',
              ),
              keyboardType: TextInputType.number,
              validator: (text) {
                final v = num.tryParse(text ?? '');
                return v == null ||
                        !v.isFinite ||
                        v < field['min'] ||
                        v > field['max'] ||
                        (!damage &&
                            (v != v.roundToDouble() ||
                                ((weapon?['allowed_values']?[field['key']]
                                            as List?)
                                        ?.contains(v.toInt()) ==
                                    false)))
                    ? '数值超出范围'
                    : null;
              },
              onChanged: (text) => setState(() {
                final changes = rule.putIfAbsent(
                  'properties',
                  () => <String, dynamic>{},
                ) as Map;
                final values = changes.putIfAbsent(
                  hit['id'],
                  () => <String, dynamic>{},
                ) as Map;
                values[field['key']] = num.tryParse(text) ?? -1;
                dirty = true;
              }),
            ),
          ),
      ],
    ),
  );

  List<Widget> hitEditors(
    dynamic stage,
    Map<String, dynamic> rule,
    bool enabled,
  ) {
    return [
      for (final hit in (stage['hits'] as List? ?? []))
        Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if ((stage['hits'] as List).length > 1)
              Text('命中 ${(stage['hits'] as List).indexOf(hit) + 1}'),
            Padding(
              padding: const EdgeInsets.all(8),
              child: DropdownButtonFormField<String>(
                key: ValueKey(
                  '$editorVersion-${rule['stage']}-${hit['id']}-${reactionChoice(hit, rule)}-effect',
                ),
                isExpanded: true,
                initialValue: reactionChoice(hit, rule),
                decoration: const InputDecoration(labelText: '受击动作'),
                items: [
                  DropdownMenuItem(
                    value: 'original',
                    child: Text('默认（${originalReaction(hit)}）'),
                  ),
                  if (reactionChoice(hit, rule) == 'custom')
                    const DropdownMenuItem(
                      value: 'custom',
                      enabled: false,
                      child: Text('自定义受击动作'),
                    ),
                  for (final effect in (data?['effects'] as List? ?? []))
                    DropdownMenuItem(
                      value: effect['id'] as String,
                      child: Text(
                        effect['id'] == 'float' ? '上升 / 悬浮' : effect['name'],
                      ),
                    ),
                ],
                onChanged: !enabled
                    ? null
                    : (value) => setState(() {
                        final changes = rule.putIfAbsent(
                          'properties',
                          () => <String, dynamic>{},
                        ) as Map;
                        final values = changes.putIfAbsent(
                          hit['id'],
                          () => <String, dynamic>{},
                        ) as Map;
                        if (value == 'original') {
                          values.removeWhere(
                            (key, _) =>
                                key != 'SkillDamage' &&
                                key != 'SkillEnhanceDamage',
                          );
                          if (values.isEmpty) changes.remove(hit['id']);
                          if (changes.isEmpty) rule.remove('properties');
                        } else {
                          final effect = (data!['effects'] as List).firstWhere(
                            (e) => e['id'] == value,
                          );
                          values.addAll(effect['values'] as Map);
                        }
                        editorVersion++;
                        dirty = true;
                      }),
              ),
            ),
            numberEditors(hit, rule, enabled, damage: true),
            ExpansionTile(
              title: const Text('击飞参数 / 高级设置'),
              children: [numberEditors(hit, rule, enabled, damage: false)],
            ),
          ],
        ),
    ];
  }

  Widget stageEditor(int index) {
    final rule = rules[index], stage = weapon!['stages'][index];
    final enabled = stage['supported'] == true && !busy;
    return Card(
      key: ValueKey("$editorVersion-${weapon!['id']}-${rule['stage']}"),
      child: Padding(
        padding: const EdgeInsets.all(14),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              stage['label'] ?? '动作说明缺失（按键待核实）',
              style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 17),
            ),
            if (stage['supported'] != true)
              Text(
                stage['reason'],
                style: const TextStyle(color: Colors.deepOrange),
              ),
            const SizedBox(height: 16),
            Row(
              children: [
                Expanded(
                  flex: 3,
                  child: DropdownButtonFormField<int>(
                    isExpanded: true,
                    key: ValueKey(
                      '${weapon!['id']}-${rule['stage']}-buff-${rule['buff']}',
                    ),
                    initialValue: rule['buff'],
                    decoration: const InputDecoration(labelText: 'DEBUFF'),
                    items: (data!['buffs'] as List)
                        .map(
                          (b) => DropdownMenuItem<int>(
                            value: b['id'],
                            child: Text(
                              b['id'] == 0
                                  ? '默认（${originalDebuff(stage)}）'
                                  : b['name'],
                            ),
                          ),
                        )
                        .toList(),
                    onChanged: enabled
                        ? (v) => setState(() {
                            rule['buff'] = v;
                            dirty = true;
                          })
                        : null,
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: DropdownButtonFormField<int>(
                    isExpanded: true,
                    key: ValueKey(
                      '${weapon!['id']}-${rule['stage']}-level-${rule['level']}',
                    ),
                    initialValue: rule['level'],
                    decoration: const InputDecoration(labelText: '等级'),
                    items: [1, 2, 3]
                        .map(
                          (v) => DropdownMenuItem(value: v, child: Text('$v')),
                        )
                        .toList(),
                    onChanged: enabled && rule['buff'] != 0
                        ? (v) => setState(() {
                            rule['level'] = v;
                            dirty = true;
                          })
                        : null,
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  flex: 2,
                  child: TextFormField(
                    key: ValueKey('$editorVersion-${rule['stage']}-duration'),
                    initialValue: '${rule['duration']}',
                    enabled: enabled && rule['buff'] != 0,
                    decoration: const InputDecoration(labelText: '持续周期（原生值）'),
                    keyboardType: TextInputType.number,
                    validator: (v) {
                      final n = int.tryParse(v ?? '');
                      return n == null || n < 1 || n > 60000
                          ? '请输入 1–60000'
                          : null;
                    },
                    onChanged: (v) => setState(() {
                      rule['duration'] = int.tryParse(v) ?? 0;
                      dirty = true;
                    }),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            ...hitEditors(stage, rule, enabled),
          ],
        ),
      ),
    );
  }

  Widget actionChoice(String key, int? index, String label) {
    return Column(
      children: [
        ListTile(
          title: Text(label),
          subtitle: index == null ? const Text('提示中的状态没有对应动作，不能编辑') : null,
          selected: selectedAction == key,
          trailing: index == null ? null : const Icon(Icons.edit_outlined),
          onTap: index == null || busy
              ? null
              : () {
                  if (!(form.currentState?.validate() ?? true)) return;
                  setState(() {
                    selectedAction = selectedAction == key ? null : key;
                  });
                },
        ),
        if (selectedAction == key && index != null) stageEditor(index),
      ],
    );
  }

  late final itemPictures = ItemPictures(widget.api);
  Widget weaponIcon(Map<dynamic, dynamic> value, double size) => itemPictures
      .preview({'key': '25:${value['id']}', 'name': value['name']}, size: size);

  @override
  Widget build(BuildContext context) {
    final allWeapons = data?['weapons'] as List? ?? [];
    final types =
        allWeapons.map((w) => '${w['type'] ?? '未分类'}').toSet().toList()..sort();
    final weapons = allWeapons
        .where(
          (w) =>
              '${w['name']} ${w['id']}'.contains(query.trim()) &&
              (weaponType == '全部类型' || '${w['type'] ?? '未分类'}' == weaponType),
        )
        .toList();
    final combos = weapon?['combos'] as List? ?? [];
    final stageIndices = <String, int>{
      for (var i = 0; i < rules.length; i++)
        '${weapon!['stages'][i]['state']}': i,
    };
    final mapped = {
      for (final c in combos)
        for (final n in c['nodes'] as List) '${n['state']}',
    };
    final otherStages = stageIndices.entries
        .where((e) => !mapped.contains(e.key))
        .map((e) => e.value)
        .toList();
    final applied = data?['applied']['${weapon?['id']}'] as List? ?? [];
    return PopScope(
      canPop: !dirty && !busy,
      onPopInvokedWithResult: (didPop, result) async {
        if (didPop || busy) return;
        if (await discard() && mounted) {
          setState(() => dirty = false);
          WidgetsBinding.instance.addPostFrameCallback((_) {
            if (mounted) Navigator.pop(context);
          });
        }
      },
      child: Scaffold(
        appBar: AppBar(
          title: const Text('武器配置'),
          actions: [
            TextButton.icon(
              onPressed: busy
                  ? null
                  : () async {
                      if (await discard() && mounted) {
                        setState(() {
                          dirty = false;
                          busy = true;
                          message = '';
                        });
                        await load();
                      }
                    },
              icon: const Icon(Icons.refresh),
              label: const Text('重新读取'),
            ),
          ],
        ),
        body: Column(
          children: [
            if (busy) const LinearProgressIndicator(),
            Expanded(
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  SizedBox(
                    width: 300,
                    child: Column(
                      children: [
                        Padding(
                          padding: const EdgeInsets.all(12),
                          child: TextField(
                            decoration: const InputDecoration(
                              labelText: '搜索武器名称 / 编号',
                              prefixIcon: Icon(Icons.search),
                            ),
                            onChanged: (value) => setState(() => query = value),
                          ),
                        ),
                        Padding(
                          padding: const EdgeInsets.symmetric(horizontal: 12),
                          child: DropdownButtonFormField<String>(
                            key: ValueKey('weapon-type-$weaponType'),
                            initialValue:
                                ['全部类型', ...types].contains(weaponType)
                                ? weaponType
                                : '全部类型',
                            decoration: const InputDecoration(
                              labelText: '武器类型',
                            ),
                            items: ['全部类型', ...types]
                                .map(
                                  (type) => DropdownMenuItem(
                                    value: type,
                                    child: Text(type),
                                  ),
                                )
                                .toList(),
                            onChanged: (value) =>
                                setState(() => weaponType = value ?? '全部类型'),
                          ),
                        ),
                        Padding(
                          padding: const EdgeInsets.all(8),
                          child: Text('共 ${weapons.length} 件武器'),
                        ),
                        Expanded(
                          child: ListView.builder(
                            itemCount: weapons.length,
                            itemBuilder: (context, index) {
                              final value = weapons[index];
                              return ListTile(
                                leading: weaponIcon(value, 40),
                                selected: weapon?['id'] == value['id'],
                                title: Text(value['name']),
                                subtitle: Text(
                                  '${value['type'] ?? '未分类'} · ${value['id']}\n${(value['stages'] as List).length} 个招式',
                                ),
                                onTap: busy
                                    ? null
                                    : () async {
                                        if (value['id'] == weapon?['id']) {
                                          return;
                                        }
                                        if (await discard() && mounted) {
                                          setState(() {
                                            select(
                                              Map<String, dynamic>.from(value),
                                            );
                                            message = '';
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
                  const VerticalDivider(width: 1),
                  Expanded(
                    child: weapon == null
                        ? Center(child: Text(busy ? '读取武器动作配置…' : '没有可读取的武器'))
                        : Padding(
                            padding: const EdgeInsets.all(20),
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                SizedBox(
                                  height: 160,
                                  child: Card(
                                    child: Padding(
                                      padding: const EdgeInsets.all(12),
                                      child: Row(
                                        crossAxisAlignment:
                                            CrossAxisAlignment.start,
                                        children: [
                                          weaponIcon(weapon!, 72),
                                          const SizedBox(width: 16),
                                          Expanded(
                                            child: Column(
                                              crossAxisAlignment:
                                                  CrossAxisAlignment.start,
                                              children: [
                                                Text(
                                                  '${weapon!['name']}',
                                                  style: Theme.of(context)
                                                      .textTheme
                                                      .titleLarge,
                                                ),
                                                Text(
                                                  '${weapon!['type'] ?? '未分类'} · ${weapon!['id']}',
                                                ),
                                                const SizedBox(height: 6),
                                                const Text(
                                                  '武器简介',
                                                  style: TextStyle(
                                                    fontWeight: FontWeight.bold,
                                                  ),
                                                ),
                                                Expanded(
                                                  child: SingleChildScrollView(
                                                    key: ValueKey(
                                                      'weapon-description-${weapon!['id']}',
                                                    ),
                                                    child: SelectableText(
                                                      '${weapon!['description'] ?? ''}'
                                                              .trim()
                                                              .isEmpty
                                                          ? '暂无武器简介'
                                                          : '${weapon!['description']}',
                                                    ),
                                                  ),
                                                ),
                                              ],
                                            ),
                                          ),
                                        ],
                                      ),
                                    ),
                                  ),
                                ),
                                const SizedBox(height: 8),
                                Text(
                                  '连招与命中效果',
                                  style: Theme.of(context)
                                      .textTheme
                                      .titleMedium,
                                ),
                                const SizedBox(height: 8),
                                Text(
                                  applied.isEmpty
                                      ? '当前游戏配置：原效果'
                                      : '当前游戏配置：已写入 ${applied.length} 段效果；需重启游戏加载',
                                ),
                                const SizedBox(height: 8),
                                const Text('选择招式，设置 DEBUFF、受击动作和伤害。'),
                                if ((weapon!['combos'] as List? ?? []).isEmpty)
                                  const Text('未收录按键提示，按动作名称选择。'),
                                if (weapon!['id'] == 253013)
                                  Align(
                                    alignment: Alignment.centerLeft,
                                    child: TextButton.icon(
                                      onPressed: busy
                                          ? null
                                          : () => setState(() {
                                              editorVersion++;
                                              for (final r in rules) {
                                                r['buff'] = r['stage'] == 1
                                                    ? 1
                                                    : r['stage'] == 2
                                                    ? 37
                                                    : 0;
                                                r['level'] = 1;
                                                r['duration'] = 3000;
                                              }
                                              dirty = true;
                                            }),
                                      icon: const Icon(Icons.auto_fix_high),
                                      label: const Text('填入示例：第一下中毒，第二下燃烧'),
                                    ),
                                  ),
                                const SizedBox(height: 10),
                                Expanded(
                                  child: Form(
                                    key: form,
                                    child: ListView.builder(
                                      itemCount:
                                          combos.length +
                                          (otherStages.isEmpty ? 0 : 1),
                                      itemBuilder: (context, group) {
                                        final other = group == combos.length;
                                        final combo = other
                                            ? null
                                            : combos[group];
                                        final nodes = other
                                            ? <dynamic>[]
                                            : combo['nodes'] as List;
                                        final title = other
                                            ? '动作说明（${otherStages.length}）'
                                            : nodes.isEmpty
                                            ? '按键提示不完整'
                                            : '${nodes.last['keys']}';
                                        return ExpansionTile(
                                          initiallyExpanded:
                                              other && combos.isEmpty,
                                          key: ValueKey(
                                            '${weapon!['id']}-combo-$group',
                                          ),
                                          title: Text(title),
                                          subtitle: Text(
                                            other
                                                ? '按动作名称选择'
                                                : '${combo['name']} · ${nodes.length} 个动作段',
                                          ),
                                          children: [
                                            for (
                                              var n = 0;
                                              n <
                                                  (other
                                                      ? otherStages.length
                                                      : nodes.length);
                                              n++
                                            )
                                              actionChoice(
                                                '$group:$n',
                                                other
                                                    ? otherStages[n]
                                                    : stageIndices['${nodes[n]['state']}'],
                                                other
                                                    ? '${weapon!['stages'][otherStages[n]]['label'] ?? '动作说明缺失（按键待核实）'}'
                                                    : '第 ${n + 1} 段 · ${nodes[n]['keys']}',
                                              ),
                                          ],
                                        );
                                      },
                                    ),
                                  ),
                                ),
                                const SizedBox(height: 10),
                                Wrap(
                                  spacing: 12,
                                  runSpacing: 8,
                                  crossAxisAlignment: WrapCrossAlignment.center,
                                  children: [
                                    OutlinedButton.icon(
                                      onPressed: busy || dirty ? null : repairEffects,
                                      icon: const Icon(Icons.auto_fix_high),
                                      label: const Text('自动补齐攻击特效'),
                                    ),
                                    OutlinedButton.icon(
                                      onPressed: busy
                                          ? null
                                          : () => execute('weapon_save'),
                                      icon: const Icon(Icons.save_outlined),
                                      label: const Text('保存方案'),
                                    ),
                                    FilledButton.icon(
                                      onPressed: busy
                                          ? null
                                          : () => execute('weapon_apply'),
                                      icon: const Icon(Icons.check),
                                      label: const Text('应用到游戏'),
                                    ),
                                    TextButton(
                                      onPressed: busy || applied.isEmpty
                                          ? null
                                          : () => execute('weapon_restore'),
                                      child: const Text('恢复原效果'),
                                    ),
                                    FilledButton.tonalIcon(
                                      onPressed: busy ? null : publish,
                                      icon: const Icon(
                                        Icons.cloud_upload_outlined,
                                      ),
                                      label: const Text('更新到线上'),
                                    ),
                                    if (dirty)
                                      const Text(
                                        '有未保存修改',
                                        style: TextStyle(
                                          color: Colors.deepOrange,
                                        ),
                                      ),
                                  ],
                                ),
                              ],
                            ),
                          ),
                  ),
                ],
              ),
            ),
            if (message.isNotEmpty)
              Container(
                width: double.infinity,
                padding: const EdgeInsets.all(14),
                color: failed ? Colors.red.shade50 : Colors.teal.shade50,
                child: SelectableText(
                  message,
                  style: TextStyle(
                    color: failed ? Colors.red.shade900 : Colors.teal.shade900,
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}
