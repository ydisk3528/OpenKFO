import 'reward_picker.dart';

import 'dart:convert';

import 'package:flutter/material.dart';

class ExtendedTaskConfigPage extends StatefulWidget {
  const ExtendedTaskConfigPage({
    super.key,
    required this.api,
    required this.environment,
    required this.initial,
    required this.canReadClient,
  });
  final Future<dynamic> Function(Map<String, dynamic>) api;
  final String environment;
  final Map<String, dynamic>? initial;
  final bool canReadClient;
  @override
  State<ExtendedTaskConfigPage> createState() => _ExtendedTaskConfigPageState();
}

class _ExtendedTaskConfigPageState extends State<ExtendedTaskConfigPage> {
  late final rewardCatalog = RewardCatalog.load(widget.api);
  static const events = {
    '': '未绑定（不自动完成）',
    'tutorial_complete': '首次完成新手引导（需提前接取）',
    'battle_play': '有效对战：参加一场',
    'battle_win': '有效对战：胜利一场',
    'survival_solo_play': '生存大乱斗：参加一场',
    'survival_team_play': '生存团队战：参加一场',
    'deathmatch_solo_play': '歼敌大乱斗：参加一场',
    'deathmatch_team_play': '歼敌团队战：参加一场',
    'survival_solo_win': '生存大乱斗：胜利',
    'survival_team_win': '生存团队战：胜利',
    'deathmatch_solo_win': '歼敌大乱斗：胜利',
    'deathmatch_team_win': '歼敌团队战：胜利',
  };
  Map<String, dynamic>? draft;
  bool busy = false;
  String kind = 'daily', status = '';
  @override
  void initState() {
    super.initState();
    draft = widget.initial == null
        ? null
        : Map<String, dynamic>.from(
            jsonDecode(jsonEncode(widget.initial)) as Map,
          );
  }

  Future<void> importCatalogue() async {
    if (busy) return;
    setState(() => busy = true);
    try {
      final data = await widget.api({'operation': 'task_extended_templates'});
      final hash = data['client_hash'] as String? ?? '';
      if (!RegExp(r'^[0-9a-f]{64}$').hasMatch(hash)) {
        throw const FormatException('客户端指纹无效');
      }
      if (draft != null && draft!['client_hash'] != hash) {
        throw const FormatException('客户端版本与已有配置不同，请先核对版本');
      }
      final entries = <Map<String, dynamic>>[];
      final ids = <int>{};
      for (final type in ['daily', 'newbie']) {
        for (final raw in data['catalogues'][type] as List) {
          final item = Map<String, dynamic>.from(raw as Map);
          final id = item['id'];
          if (id is! int ||
              id > 65535 ||
              (type == 'daily' ? id < 2000 || id > 3000 : id <= 3000) ||
              !ids.add(id)) {
            throw const FormatException('任务编号或类别无效');
          }
          final conditions = (item['conditions'] as List)
              .map((e) => Map<String, dynamic>.from(e as Map))
              .toList();
          if (conditions.length != 3) throw const FormatException('任务条件必须三项');
          final keys = <int>{};
          for (final c in conditions) {
            final key = c['key'], required = c['required'];
            if (key is! int ||
                key < 0 ||
                key > 4294967295 ||
                required is! int ||
                required < 0 ||
                required > 65535 ||
                (required > 0 && !keys.add(key))) {
              throw const FormatException('任务条件无效');
            }
          }
          for (final old in (draft?['catalogue'] as List?) ?? []) {
            if (old['id'] != id || old['kind'] != type) continue;
            final previous = old['conditions'] as List;
            for (var i = 0; i < conditions.length && i < previous.length; i++) {
              if (previous[i]['key'] == conditions[i]['key'] &&
                  previous[i]['required'] == conditions[i]['required']) {
                conditions[i]['event'] = previous[i]['event'] ?? '';
              }
            }
          }
          entries.add({
            'kind': type,
            'id': id,
            'name': item['name'] as String,
            'conditions': conditions,
          });
        }
      }
      if (entries.isEmpty || entries.length > 1024) {
        throw const FormatException('任务目录数量无效');
      }
      final tasks = ((draft?['tasks'] as List?) ?? [])
          .map((e) => Map<String, dynamic>.from(e as Map))
          .toList();
      if (tasks.any((r) => !ids.contains(r['id']))) {
        throw const FormatException('已有任务不在新目录中');
      }
      final existing = tasks.map((r) => r['id']).toSet();
      for (final e in entries) {
        if (!existing.contains(e['id'])) {
          tasks.add({
            'kind': e['kind'],
            'id': e['id'],
            'enabled': false,
            'experience': 0,
            'gold': 0,
            'reward_catalog': 0,
          });
        }
      }
      if (!mounted) return;
      setState(() {
        draft = {'client_hash': hash, 'catalogue': entries, 'tasks': tasks};
        status = '已导入${entries.length}项；已有奖励保留，新增项关闭。尚未保存。';
      });
    } catch (e) {
      if (mounted) setState(() => status = '读取失败：$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> edit(Map<String, dynamic> row, Map entry) async {
    final conditions = (entry['conditions'] as List)
        .map((c) => Map<String, dynamic>.from(c as Map))
        .toList();
    final fields = {
      for (final key in ['experience', 'gold', 'reward_catalog'])
        key: '${row[key] ?? 0}',
    };
    var enabled = row['enabled'] == true;
    var error = '';
    final result = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, update) => AlertDialog(
          title: Text('任务 ${row['id']}'),
          content: SizedBox(
            width: 420,
            child: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text('条件来自客户端，保持不变：${entry['conditions']}'),
                  const Text('事件绑定是本服规则；目前仅支持至少两人的四种基础对战。训练、闯关等条件请保持未绑定。'),
                  for (var i = 0; i < conditions.length; i++)
                    if ((conditions[i]['required'] as int) > 0)
                      DropdownButtonFormField<String>(
                        key: ValueKey('extended_event_$i'),
                        initialValue: conditions[i]['event'] as String? ?? '',
                        isExpanded: true,
                        decoration: InputDecoration(
                          labelText: '条件 ${conditions[i]['key']} 的服务器事件',
                        ),
                        items: [
                          for (final e in events.entries)
                            DropdownMenuItem(
                              value: e.key,
                              child: Text(e.value),
                            ),
                        ],
                        onChanged: (v) =>
                            update(() => conditions[i]['event'] = v ?? ''),
                      ),
                  SwitchListTile(
                    title: const Text('启用此规则'),
                    value: enabled,
                    onChanged: (v) => update(() => enabled = v),
                  ),
                  for (final e in fields.entries)
                    TextFormField(
                      key: ValueKey('extended_${e.key}'),
                      initialValue: e.value,
                      onChanged: (v) => update(() => fields[e.key] = v),
                      keyboardType: TextInputType.number,
                      decoration: InputDecoration(
                        labelText: {
                          'experience': '奖励经验',
                          'gold': '奖励金币',
                          'reward_catalog': '奖励商品编号（0为无）',
                        }[e.key],
                      ),
                    ),
                  RewardPreviews(
                    catalog: rewardCatalog,
                    ids: [int.tryParse(fields['reward_catalog'] ?? '') ?? 0],
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
            FilledButton(
              onPressed: () {
                final next = {...row, 'enabled': enabled};
                for (final e in fields.entries) {
                  final n = int.tryParse(e.value.trim());
                  if (n == null ||
                      n < 0 ||
                      n >
                          (e.key == 'reward_catalog'
                              ? 4294967295
                              : 2147483647)) {
                    update(() => error = '奖励数值无效');
                    return;
                  }
                  next[e.key] = n;
                }
                if (enabled &&
                    !(entry['conditions'] as List).any(
                      (c) => (c['required'] as int) > 0,
                    )) {
                  update(() => error = '启用任务必须有有效条件');
                  return;
                }
                Navigator.pop(context, {...next, '_conditions': conditions});
              },
              child: const Text('应用'),
            ),
          ],
        ),
      ),
    );
    if (!mounted || result == null) return;
    setState(() {
      entry['conditions'] = result.remove('_conditions');
      final tasks = draft!['tasks'] as List;
      tasks[tasks.indexOf(row)] = result;
      status = '有未保存的修改';
    });
  }

  @override
  Widget build(BuildContext context) {
    final entries = {
      for (final e in (draft?['catalogue'] as List?) ?? []) e['id']: e as Map,
    };
    final rows = ((draft?['tasks'] as List?) ?? [])
        .where((r) => r['kind'] == kind)
        .toList();
    return Scaffold(
      appBar: AppBar(title: Text('每日／新手任务 · ${widget.environment}')),
      body: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          children: [
            const Text(
              '需要部署新版服务器：按绑定的服务器事件累计进度，完成后手动领奖；每日任务按 UTC 日期分周期（北京时间08:00换日），新手任务只领一次。接取后条件和奖励数值保留原规则，关闭任务会停止进度与领奖。商品奖励填写商城商品编号，领取时商品须开放；规格及期限按领取时商城配置。游戏面板奖励文字来自客户端模板，可能与这里的自定义奖励不同。',
            ),
            SegmentedButton<String>(
              segments: const [
                ButtonSegment(value: 'daily', label: Text('每日任务')),
                ButtonSegment(value: 'newbie', label: Text('新手任务')),
              ],
              selected: {kind},
              onSelectionChanged: (s) => setState(() => kind = s.first),
            ),
            Expanded(
              child: ListView.builder(
                itemCount: rows.length,
                itemBuilder: (context, i) {
                  final row = rows[i] as Map<String, dynamic>,
                      entry = entries[row['id']] ?? {};
                  return ListTile(
                    title: Text(
                      '${row['id']} ${entry['name'] ?? ''} · ${row['enabled'] == true ? '启用' : '关闭'}',
                    ),
                    subtitle: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          '经验 ${row['experience']} · 金币 ${row['gold']} · 商品 ${row['reward_catalog'] ?? 0}',
                        ),
                        RewardPreviews(
                          catalog: rewardCatalog,
                          ids: [(row['reward_catalog'] as int?) ?? 0],
                        ),
                      ],
                    ),
                    onTap: busy ? null : () => edit(row, entry),
                  );
                },
              ),
            ),
            Text(status),
            Wrap(
              spacing: 12,
              children: [
                if (widget.canReadClient)
                  TextButton(
                    onPressed: busy ? null : importCatalogue,
                    child: const Text('读取每日／新手目录'),
                  ),
                FilledButton(
                  onPressed: busy || draft == null
                      ? null
                      : () => Navigator.pop(context, draft),
                  child: const Text('应用到任务配置（待保存）'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
