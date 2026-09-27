import 'reward_picker.dart';

import 'package:flutter/material.dart';

class TitleConfigPage extends StatefulWidget {
  const TitleConfigPage({
    super.key,
    required this.api,
    required this.environment,
    this.canReadClient = true,
  });
  final Future<dynamic> Function(Map<String, dynamic>) api;
  final String environment;
  final bool canReadClient;
  @override
  State<TitleConfigPage> createState() => _TitleConfigPageState();
}

class _TitleConfigPageState extends State<TitleConfigPage> {
  late final rewardCatalog = RewardCatalog.load(widget.api);
  List<Map<String, dynamic>> rows = [];
  Map<int, String> names = {};
  String clientHash = '';
  int? revision;
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
    final rules = data['rules'] as Map;
    final next = ((rules['titles'] as List?) ?? [])
        .map((r) => Map<String, dynamic>.from(r as Map))
        .toList();
    final version = data['revision'] as int;
    final catalogue = (rules['catalogue'] as List?) ?? [];
    final nextNames = {
      for (final r in catalogue) r['level'] as int: r['name'] as String,
    };
    if (!mounted) return;
    setState(() {
      rows = next;
      enabled = rules['enabled'] == true;
      revision = version;
      clientHash = rules['client_hash'] as String? ?? '';
      names = nextNames;
      status = '已读取 ${widget.environment}，版本 $revision';
    });
  }

  Future<void> load() => run(() async {
    setState(() => revision = null);
    apply(await widget.api({'operation': 'titles_get'}));
  });
  Future<void> save() => run(() async {
    if (revision == null) return;
    if (enabled && rows.isEmpty) throw const FormatException('启用前请添加称号规则');
    if (names.isNotEmpty &&
        rows.any(
          (r) => r['enabled'] == true && !names.containsKey(r['level']),
        )) {
      throw const FormatException('存在客户端目录中没有的启用称号，请核对等级');
    }
    apply(
      await widget.api({
        'operation': 'titles_save',
        'titles': {
          'revision': revision,
          'rules': {
            'enabled': enabled,
            'titles': rows,
            'client_hash': clientHash,
            'catalogue': [
              for (final e in names.entries) {'level': e.key, 'name': e.value},
            ],
          },
        },
      }),
    );
    if (mounted) {
      setState(() => status = '已保存到 ${widget.environment}；已发放的待领资格保留原候选奖励');
    }
  });
  Future<void> importCatalogue() => run(() async {
    if (revision == null) return;
    final data = await widget.api({'operation': 'title_catalog'});
    final parsed = <int, String>{};
    for (final item in data['titles'] as List) {
      final level = item['level'];
      final name = item['name'];
      if (level is! int ||
          level < 0 ||
          level > 255 ||
          name is! String ||
          name.trim().isEmpty ||
          parsed.containsKey(level)) {
        throw const FormatException('客户端称号目录存在错误或重复条目');
      }
      parsed[level] = name;
    }
    if (parsed.isEmpty) throw const FormatException('客户端称号目录为空');
    final hash = data['client_hash'];
    if (hash is! String || !RegExp(r'^[0-9a-f]{64}$').hasMatch(hash)) {
      throw const FormatException('客户端目录缺少有效版本指纹，请更新管理组件');
    }
    final existing = rows.map((r) => r['level']).toSet();
    final additions = [
      for (final level in parsed.keys)
        if (level != 0 && !existing.contains(level))
          <String, dynamic>{
            'level': level,
            'enabled': false,
            'min_player_level': 0,
            'completed_task': 0,
            'matches': 0,
            'wins': 0,
            'choices': <int>[],
          },
    ];
    if (rows.length + additions.length > 255) {
      throw const FormatException('合并后超过255项');
    }
    if (!mounted) return;
    setState(() {
      names = parsed;
      clientHash = hash;
      rows = [...rows, ...additions]
        ..sort((a, b) => (a['level'] as int).compareTo(b['level'] as int));
      status =
          '已读取${parsed.length}个称号，补充${additions.length}条关闭的规则；未保存，条件与奖励需自行配置';
    });
  });
  Future<void> edit([int? index]) async {
    final value = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _TitleEditor(
        catalog: rewardCatalog,
        initial: index == null ? null : rows[index],
        existing: {
          for (var i = 0; i < rows.length; i++)
            if (i != index) rows[i]['level'] as int,
        },
      ),
    );
    if (value == null || !mounted) return;
    setState(() {
      if (index == null) {
        rows.add(value);
      } else {
        rows[index] = value;
      }
      rows.sort((a, b) => (a['level'] as int).compareTo(b['level'] as int));
      status = '有未保存的修改';
    });
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: Text('称号规则 · ${widget.environment}')),
    body: Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        children: [
          const Text(
            '所有非零条件须同时满足，按称号等级顺序判定。候选奖励填写商城商品编号，玩家选择一件；留空只晋升称号。导入目录随规则保存，仅在客户端配置包版本匹配时生效。关闭保留已有资格；奖励为自定义规则。',
          ),
          SwitchListTile(
            title: const Text('称号总开关'),
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
                    '称号 ${r['level']} · ${r['enabled'] == true ? '启用' : '关闭'}',
                  ),
                  subtitle: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        '${names[r['level']] ?? ''}\n角色等级 ${r['min_player_level'] ?? 0} · 任务 ${r['completed_task'] ?? 0} · 参赛 ${r['matches'] ?? 0} · 胜场 ${r['wins'] ?? 0}\n候选商品 ${(r['choices'] as List? ?? []).join(', ')}',
                      ),
                      RewardPreviews(
                        catalog: rewardCatalog,
                        ids: List<int>.from(r['choices'] as List? ?? []),
                      ),
                    ],
                  ),
                  onTap: busy || revision == null ? null : () => edit(i),
                  trailing: IconButton(
                    tooltip: '删除称号 ${r['level']}',
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
          const SizedBox(height: 8),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              if (widget.canReadClient)
                OutlinedButton(
                  onPressed: busy || revision == null ? null : importCatalogue,
                  child: const Text('读取客户端称号并补充缺项'),
                ),
              OutlinedButton(
                onPressed: busy ? null : load,
                child: const Text('重新读取'),
              ),
              OutlinedButton(
                onPressed: busy || revision == null || rows.length >= 255
                    ? null
                    : () => edit(),
                child: const Text('新增称号规则'),
              ),
              FilledButton(
                onPressed: busy || revision == null ? null : save,
                child: const Text('保存称号配置'),
              ),
            ],
          ),
        ],
      ),
    ),
  );
}

class _TitleEditor extends StatefulWidget {
  const _TitleEditor({
    required this.catalog,
    required this.initial,
    required this.existing,
  });
  final Map<String, dynamic>? initial;
  final Future<RewardCatalog> catalog;
  final Set<int> existing;
  @override
  State<_TitleEditor> createState() => _TitleEditorState();
}

class _TitleEditorState extends State<_TitleEditor> {
  static const labels = {
    'level': '称号等级（1–255）',
    'min_player_level': '最低角色等级（0忽略）',
    'completed_task': '已完成任务ID（0忽略）',
    'matches': '累计参赛数（0忽略）',
    'wins': '累计胜场数（0忽略）',
    'choices': '候选商品编号（逗号分隔，最多7个）',
  };
  final fields = <String, TextEditingController>{};
  bool enabled = false;
  String error = '';
  @override
  void initState() {
    super.initState();
    final r = widget.initial ?? {};
    enabled = r['enabled'] == true;
    for (final k in labels.keys) {
      fields[k] = TextEditingController(
        text: k == 'choices'
            ? (r[k] as List? ?? []).join(',')
            : '${r[k] ?? (k == 'level' ? 1 : 0)}',
      );
    }
  }

  @override
  void dispose() {
    for (final c in fields.values) {
      c.dispose();
    }
    super.dispose();
  }

  void submit() {
    try {
      final row = <String, dynamic>{'enabled': enabled};
      for (final k in labels.keys.where((k) => k != 'choices')) {
        final v = int.tryParse(fields[k]!.text.trim());
        final max = k == 'level'
            ? 255
            : k == 'min_player_level'
            ? 200
            : k == 'completed_task'
            ? 65535
            : 2147483647;
        if (v == null ||
            v < 0 ||
            v > max ||
            (k == 'level' && (v == 0 || widget.existing.contains(v)))) {
          throw FormatException('${labels[k]}无效或重复');
        }
        row[k] = v;
      }
      final raw = fields['choices']!.text.trim();
      final choices = raw.isEmpty
          ? <int>[]
          : raw.split(RegExp(r'[,，\s]+')).map((s) => int.parse(s)).toList();
      if (choices.length > 7 ||
          choices.toSet().length != choices.length ||
          choices.any((v) => v <= 0 || v > 4294967295)) {
        throw const FormatException('最多7个非零、不重复的商品编号；留空只晋升称号');
      }
      if (enabled &&
          [
            'min_player_level',
            'completed_task',
            'matches',
            'wins',
          ].every((k) => row[k] == 0)) {
        throw const FormatException('启用称号至少需要一个达成条件');
      }
      row['choices'] = choices;
      Navigator.pop(context, row);
    } catch (e) {
      setState(() => error = '输入错误：$e');
    }
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: Text(widget.initial == null ? '新增称号规则' : '编辑称号规则'),
    content: SizedBox(
      width: 480,
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            SwitchListTile(
              title: const Text('启用此称号'),
              value: enabled,
              onChanged: (v) => setState(() => enabled = v),
            ),
            for (final entry in labels.entries)
              Padding(
                padding: const EdgeInsets.only(bottom: 12),
                child: TextField(
                  key: ValueKey('title-${entry.key}'),
                  controller: fields[entry.key],
                  onChanged: (_) => setState(() {}),
                  decoration: InputDecoration(labelText: entry.value),
                ),
              ),
            RewardPreviews(
              catalog: widget.catalog,
              ids: fields['choices']!.text
                  .split(RegExp(r'[,，\s]+'))
                  .map((s) => int.tryParse(s) ?? 0)
                  .toList(),
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
      FilledButton(onPressed: submit, child: const Text('确定')),
    ],
  );
}
