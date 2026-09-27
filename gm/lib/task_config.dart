import 'reward_picker.dart';

import 'package:flutter/material.dart';

import 'extended_task_config.dart';

class TaskConfigPage extends StatefulWidget {
  const TaskConfigPage({
    super.key,
    required this.api,
    required this.environment,
    this.canReadClient = true,
  });
  final Future<dynamic> Function(Map<String, dynamic>) api;
  final String environment;
  final bool canReadClient;
  @override
  State<TaskConfigPage> createState() => _TaskConfigPageState();
}

class _TaskConfigPageState extends State<TaskConfigPage> {
  late final rewardCatalog = RewardCatalog.load(widget.api);
  List<Map<String, dynamic>> rows = [];
  Map<int, Map<String, dynamic>> templates = {};
  int? revision;
  String clientHash = "";
  List<Map<String, dynamic>> catalogue = [];
  Map<String, dynamic>? extended;
  bool enabled = false, busy = false;
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
    final rule = data['rules'] as Map;
    final next = ((rule['tasks'] as List?) ?? [])
        .map((r) => Map<String, dynamic>.from(r as Map))
        .toList();
    for (final r in next) {
      if (r['counters'] is! List || (r['counters'] as List).length != 29) {
        throw const FormatException('任务统计条件不是29项，请检查管理服务版本');
      }
    }
    final version = data['revision'] as int;
    if (!mounted) return;
    setState(() {
      rows = next;
      extended = rule['extended'] == null
          ? null
          : Map<String, dynamic>.from(rule['extended'] as Map);
      clientHash = rule['client_hash'] as String? ?? '';
      catalogue = ((rule['catalogue'] as List?) ?? [])
          .map((v) => Map<String, dynamic>.from(v as Map))
          .toList();
      enabled = rule['enabled'] == true;
      revision = version;
      status = '已读取 ${widget.environment}，版本 $revision';
    });
  }

  Future<void> load() => run(() async {
    setState(() => revision = null);
    apply(await widget.api({'operation': 'tasks_get'}));
  });
  Future<void> importTemplates() => run(() async {
    if (revision == null) return;
    final data = await widget.api({'operation': 'task_templates'});
    final hash = data['client_hash'] as String? ?? '';
    if (!RegExp(r'^[0-9a-f]{64}$').hasMatch(hash)) {
      throw const FormatException('任务模板缺少有效客户端指纹');
    }
    final source = data['templates'] as List;
    final parsed = <int, Map<String, dynamic>>{};
    int number(dynamic value, int max) {
      if (value is! int || value < 0 || value > max) {
        throw const FormatException('客户端任务模板包含无效数值');
      }
      return value;
    }

    for (final item in source) {
      final t = Map<String, dynamic>.from(item as Map);
      final id = number(t['id'], 65535);
      if (id == 0 ||
          parsed.containsKey(id) ||
          t['name'] is! String ||
          (t['name'] as String).trim().isEmpty) {
        throw const FormatException('客户端任务模板编号或名称无效');
      }
      number(t['next'], 65535);
      number(t['title_level'], 255);
      if (t['enabled'] is! bool) throw const FormatException('任务模板启用字段无效');
      number(t['matches'], 2147483647);
      number(t['max_combo'], 2147483647);
      final counters = t['counters'] as List;
      if (counters.length != 29) {
        throw const FormatException('客户端任务统计条件不是29项');
      }
      for (final v in counters) {
        number(v, 2147483647);
      }
      parsed[id] = t;
    }
    if (parsed.isEmpty) throw const FormatException('客户端没有基础任务模板');
    for (final start in parsed.keys) {
      final seen = <int>{};
      var id = start;
      while (id != 0) {
        if (!parsed.containsKey(id) || !seen.add(id)) {
          throw const FormatException('客户端任务模板后续关系无效');
        }
        id = parsed[id]!['next'] as int;
      }
    }
    final existing = rows.map((r) => r['id']).toSet();
    final additions = [
      for (final t in parsed.values)
        if (!existing.contains(t['id']))
          <String, dynamic>{
            'id': t['id'],
            'next': t['next'],
            'matches': t['matches'],
            'max_combo': t['max_combo'],
            'counters': List<int>.from(t['counters'] as List),
            'enabled': false,
            'experience': 0,
            'gold': 0,
          },
    ];
    if (rows.length + additions.length > 512) {
      throw const FormatException('合并后超过512项，请先整理任务');
    }
    if (!mounted) return;
    setState(() {
      templates = parsed;
      clientHash = hash;
      catalogue = parsed.values
          .map(
            (t) => <String, dynamic>{
              'id': t['id'],
              'next': t['next'],
              'title_level': t['title_level'],
              'enabled': t['enabled'],
            },
          )
          .toList();
      rows = [...rows, ...additions];
      status =
          '读取${parsed.length}项模板，新增${additions.length}项待保存；已有配置保留，新任务关闭且奖励为0';
    });
  });
  Future<void> save() => run(() async {
    if (revision == null) return;
    final byID = {for (final r in rows) r['id'] as int: r};
    if (enabled && rows.isEmpty) throw const FormatException('启用前请添加任务');
    for (final r in rows) {
      final seen = <int>{};
      var id = r['id'] as int;
      while (id != 0) {
        if (!byID.containsKey(id) || !seen.add(id)) {
          throw const FormatException('后续任务不存在或形成循环，请检查任务链');
        }
        id = byID[id]!['next'] as int;
      }
    }
    apply(
      await widget.api({
        'operation': 'tasks_save',
        'tasks': {
          'revision': revision,
          'rules': {
            'enabled': enabled,
            'tasks': rows,
            'client_hash': clientHash,
            'catalogue': catalogue,
            if (extended != null) 'extended': extended,
          },
        },
      }),
    );
    if (mounted) {
      setState(() => status = '配置已保存到 ${widget.environment}；已接任务保留接取时的条件和奖励');
    }
  });
  Future<void> edit([int? index]) async {
    final row = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _TaskEditor(
        catalog: rewardCatalog,
        initial: index == null ? null : rows[index],
        template: index == null ? null : templates[rows[index]['id']],
        existing: {
          for (var i = 0; i < rows.length; i++)
            if (i != index) rows[i]['id'] as int,
        },
      ),
    );
    if (row == null || !mounted) return;
    setState(() {
      if (index == null) {
        rows.add(row);
      } else {
        rows[index] = row;
      }
      status = '有未保存的修改';
    });
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: Text('任务配置 · ${widget.environment}')),
    body: Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        children: [
          const Text(
            '新版服务器刷新任务时自动发放已达标的参赛／胜场任务奖励。连击、歼敌及其他统计条件尚不支持完成。任务编号与条件需匹配客户端 BaseQuest；奖励为自定义规则。物品奖励填写商城商品编号（不是道具ID），每项任务发放一份商品规格；领取时商品须开放，规格和期限按当时商城配置。',
          ),
          SwitchListTile(
            title: const Text('基础任务开关（每日／新手单独配置）'),
            value: enabled,
            onChanged: busy || revision == null
                ? null
                : (v) => setState(() {
                    enabled = v;
                    status = '有未保存的修改';
                  }),
          ),
          Expanded(
            child: ListView.builder(
              itemCount: rows.length,
              itemBuilder: (context, i) {
                final r = rows[i];
                return ListTile(
                  title: Text(
                    '任务 ${r['id']} · ${r['enabled'] == true ? '启用' : '关闭'}',
                  ),
                  subtitle: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        '${templates[r['id']]?['name'] ?? ''}\n参赛 ${r['matches']} · 连击 ${r['max_combo']} · 经验 ${r['experience']} · 金币 ${r['gold']} · 奖励商品 ${r['reward_catalog'] ?? 0} · 后续 ${r['next']}',
                      ),
                      RewardPreviews(
                        catalog: rewardCatalog,
                        ids: [(r['reward_catalog'] as int?) ?? 0],
                      ),
                    ],
                  ),
                  onTap: busy || revision == null ? null : () => edit(i),
                  trailing: IconButton(
                    tooltip: '删除任务 ${r['id']}',
                    icon: const Icon(Icons.delete_outline),
                    onPressed: busy || revision == null
                        ? null
                        : () => setState(() {
                            rows.removeAt(i);
                            status = '有未保存的修改';
                          }),
                  ),
                );
              },
            ),
          ),
          Text(status),
          Wrap(
            spacing: 12,
            children: [
              TextButton(
                onPressed: busy ? null : load,
                child: const Text('重新读取'),
              ),
              if (widget.canReadClient)
                TextButton(
                  onPressed: busy || revision == null ? null : importTemplates,
                  child: const Text('读取客户端模板并补充缺项'),
                ),
              TextButton(
                onPressed: busy || revision == null
                    ? null
                    : () async {
                        final value =
                            await Navigator.push<Map<String, dynamic>>(
                              context,
                              MaterialPageRoute(
                                builder: (_) => ExtendedTaskConfigPage(
                                  api: widget.api,
                                  environment: widget.environment,
                                  initial: extended,
                                  canReadClient: widget.canReadClient,
                                ),
                              ),
                            );
                        if (value != null && mounted) {
                          setState(() {
                            extended = value;
                            status = '每日／新手规则已更新到草稿，请点击保存任务配置';
                          });
                        }
                      },
                child: const Text('每日／新手任务'),
              ),
              TextButton(
                onPressed: busy || revision == null || rows.length >= 512
                    ? null
                    : () => edit(),
                child: const Text('新增任务'),
              ),
              FilledButton(
                onPressed: busy || revision == null ? null : save,
                child: const Text('保存任务配置'),
              ),
            ],
          ),
        ],
      ),
    ),
  );
}

class _TaskEditor extends StatefulWidget {
  const _TaskEditor({
    required this.catalog,
    required this.initial,
    required this.existing,
    this.template,
  });
  final Map<String, dynamic>? initial;
  final Map<String, dynamic>? template;
  final Future<RewardCatalog> catalog;
  final Set<int> existing;
  @override
  State<_TaskEditor> createState() => _TaskEditorState();
}

class _TaskEditorState extends State<_TaskEditor> {
  static const names = {
    'id': '任务ID',
    'next': '后续任务ID（0为结束）',
    'matches': '总参赛次数',
    'max_combo': '最大连击数',
    'experience': '奖励经验',
    'gold': '奖励金币',
    'reward_catalog': '奖励商品编号（0为不发道具）',
  };
  static const counterNames = [
    '总歼敌数',
    '生存大乱斗参加次数',
    '生存大乱斗胜利次数',
    '生存团队战参加次数',
    '生存团队战胜利次数',
    '歼敌大乱斗参加次数',
    '歼敌大乱斗胜利次数',
    '歼敌团队战参加次数',
    '歼敌团队战胜利次数',
  ];
  final fields = <String, TextEditingController>{};
  final counters = <TextEditingController>[];
  bool enabled = false;
  String error = '';
  @override
  void initState() {
    super.initState();
    for (final name in names.keys) {
      fields[name] = TextEditingController(
        text: '${widget.initial?[name] ?? 0}',
      );
    }
    for (var i = 0; i < 29; i++) {
      counters.add(
        TextEditingController(text: '${widget.initial?['counters'][i] ?? 0}'),
      );
    }
    enabled = widget.initial?['enabled'] == true;
  }

  @override
  void dispose() {
    for (final c in [...fields.values, ...counters]) {
      c.dispose();
    }
    super.dispose();
  }

  int number(TextEditingController c, int max) {
    final v = int.tryParse(c.text.trim());
    if (v == null || v < 0 || v > max) throw FormatException('请输入0至$max的整数');
    return v;
  }

  void submit() {
    try {
      final result = <String, dynamic>{'enabled': enabled};
      for (final e in fields.entries) {
        result[e.key] = number(
          e.value,
          e.key == 'id' || e.key == 'next'
              ? 65535
              : e.key == 'reward_catalog'
              ? 4294967295
              : 2147483647,
        );
      }
      if (result['id'] == 0 || widget.existing.contains(result['id'])) {
        throw const FormatException('任务ID必须非零且不重复');
      }
      if (result['next'] == result['id']) {
        throw const FormatException('后续任务不能指向自己');
      }
      result['counters'] = counters.map((c) => number(c, 2147483647)).toList();
      if (enabled &&
          result['matches'] == 0 &&
          result['max_combo'] == 0 &&
          (result['counters'] as List<int>).every((v) => v == 0)) {
        throw const FormatException('启用任务必须有完成条件');
      }
      Navigator.pop(context, result);
    } catch (e) {
      setState(() => error = '$e');
    }
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: Text(widget.initial == null ? '新增任务' : '编辑任务'),
    content: SizedBox(
      width: 480,
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (widget.template != null)
              Text(
                '${widget.template!['name']}\n${widget.template!['description'] ?? ''}\n模板仅用于核对条件，经验金币需自行配置；未导入原始奖励及物品字段。',
              ),
            SwitchListTile(
              title: const Text('启用此任务'),
              value: enabled,
              onChanged: (v) => setState(() => enabled = v),
            ),
            for (final e in names.entries)
              TextField(
                key: ValueKey('task_${e.key}'),
                controller: fields[e.key],
                onChanged: (_) => setState(() {}),
                keyboardType: TextInputType.number,
                decoration: InputDecoration(labelText: e.value),
              ),
            RewardPreviews(
              catalog: widget.catalog,
              ids: [int.tryParse(fields['reward_catalog']!.text) ?? 0],
            ),
            ExpansionTile(
              title: const Text('29项统计条件（0表示不要求）'),
              maintainState: true,
              children: [
                for (var i = 0; i < 29; i++)
                  TextField(
                    key: ValueKey('counter_$i'),
                    controller: counters[i],
                    keyboardType: TextInputType.number,
                    decoration: InputDecoration(
                      labelText: i < counterNames.length
                          ? counterNames[i]
                          : '统计索引 $i（需核对客户端）',
                    ),
                  ),
              ],
            ),
            if (error.isNotEmpty) Text(error),
          ],
        ),
      ),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('取消'),
      ),
      FilledButton(onPressed: submit, child: const Text('应用到列表')),
    ],
  );
}
