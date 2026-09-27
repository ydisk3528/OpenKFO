import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'reward_table.dart';
import 'drop_config.dart';
import 'level_gift_config.dart';
import 'item_definitions.dart';
import 'stage_reward_config.dart';

typedef RewardApi = Future<dynamic> Function(Map<String, dynamic>);

class RewardConfigPage extends StatefulWidget {
  const RewardConfigPage({
    super.key,
    required this.api,
    required this.environment,
    this.environmentApi,
    this.textCsv = false,
  });
  final bool textCsv;
  final RewardApi api;
  final RewardApi? environmentApi;
  final String environment;
  @override
  State<RewardConfigPage> createState() => _RewardConfigPageState();
}

class _RewardConfigPageState extends State<RewardConfigPage> {
  List<Map<String, int>> rows = [];
  List<Map<String, dynamic>> drops = [];
  List<Map<String, dynamic>> levelGifts = [];
  List<Map<String, dynamic>> stageRewards = [];
  Map<String, dynamic> tutorial = {"items": <int>[], "gold": 0, "tickets": 0};
  int? revision;
  bool busy = false, growth = false, dirty = false;
  String status = '';
  Map<String, dynamic> get rules => {
    'growth_enabled': growth,
    'levels': rows,
    'drops': drops,
    'level_gifts': levelGifts,
    'tutorial_reward': tutorial,
    'stage_rewards': stageRewards,
  };
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

  void apply(dynamic r) {
    stageRewards = (r['rules']['stage_rewards'] as List? ?? [])
        .map((e) => Map<String, dynamic>.from(e))
        .toList();
    rows = rewardRows(Map<String, dynamic>.from(r['rules']));
    drops = (r['rules']['drops'] as List? ?? [])
        .map((e) => Map<String, dynamic>.from(e))
        .toList();
    levelGifts = (r['rules']['level_gifts'] as List? ?? [])
        .map((e) => Map<String, dynamic>.from(e))
        .toList();
    tutorial = Map<String, dynamic>.from(
      r['rules']['tutorial_reward'] ??
          {'items': <int>[], 'gold': 0, 'tickets': 0},
    );
    growth = r['rules']['growth_enabled'] == true;
    revision = r['revision'] as int;
    dirty = false;
  }

  Future<bool> confirm(String title, String content) async =>
      await showDialog<bool>(
        context: context,
        builder: (c) => AlertDialog(
          title: Text(title),
          content: SizedBox(
            width: 720,
            height: 400,
            child: SingleChildScrollView(child: SelectableText(content)),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(c, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(c, true),
              child: const Text('确认'),
            ),
          ],
        ),
      ) ??
      false;
  Future<void> load() async {
    if (dirty && !await confirm('丢弃未保存修改？', '重新读取将覆盖本页未保存的修改。')) return;
    await run(() async {
      final r = await widget.api({'operation': 'rewards_get'});
      if (!mounted) return;
      setState(() {
        apply(r);
        status = '已读取${widget.environment}，共 200 级';
      });
    });
  }

  Future<void> save() => run(() async {
    validateRewardRows(rows, growth: growth);
    final r = await widget.api({
      'operation': 'rewards_save',
      'reward_revision': revision,
      'rewards': rules,
    });
    if (mounted) {
      setState(() {
        apply(r);
        status = '已保存到${widget.environment}；下一次结算生效，已结算对局不补发。';
      });
    }
  });
  Future<void> edit({int? index}) async {
    final first = TextEditingController(text: '${(index ?? 0) + 1}'),
        last = TextEditingController(
          text: '${index == null ? 200 : index + 1}',
        );
    final fields = {
      for (final k in rewardColumns.keys.skip(1))
        k: TextEditingController(
          text: index == null ? '' : '${rows[index][k]}',
        ),
    };
    String error = '';
    final result = await showDialog<bool>(
      context: context,
      builder: (c) => StatefulBuilder(
        builder: (c, refresh) => AlertDialog(
          title: Text(index == null ? '批量修改等级区间' : '修改第 ${index + 1} 级'),
          content: SizedBox(
            width: 560,
            child: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  if (index == null)
                    Row(
                      children: [
                        Expanded(
                          child: TextField(
                            controller: first,
                            decoration: const InputDecoration(
                              labelText: '起始等级',
                            ),
                          ),
                        ),
                        const SizedBox(width: 16),
                        Expanded(
                          child: TextField(
                            controller: last,
                            decoration: const InputDecoration(
                              labelText: '结束等级',
                            ),
                          ),
                        ),
                      ],
                    ),
                  if (index == null)
                    const Text('留空表示保持原值；填 0 表示清零。200 级升级经验始终为 0。'),
                  for (final e in fields.entries)
                    TextField(
                      key: ValueKey(e.key),
                      controller: e.value,
                      keyboardType: TextInputType.number,
                      decoration: InputDecoration(
                        labelText: rewardColumns[e.key],
                      ),
                    ),
                  Text(error, style: const TextStyle(color: Colors.red)),
                ],
              ),
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(c, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () {
                try {
                  final start = int.parse(first.text),
                      end = int.parse(last.text);
                  if (start < 1 || end > 200 || start > end) {
                    throw const FormatException('等级区间须在 1–200 内');
                  }
                  final next = rows
                      .map((r) => Map<String, int>.from(r))
                      .toList();
                  for (var i = start - 1; i < end; i++) {
                    for (final e in fields.entries) {
                      if (e.value.text.trim().isNotEmpty) {
                        next[i][e.key] = int.parse(e.value.text.trim());
                      }
                    }
                    if (i == 199) next[i]['next_experience'] = 0;
                  }
                  validateRewardRows(next);
                  rows = next;
                  Navigator.pop(c, true);
                } catch (e) {
                  refresh(() => error = '$e');
                }
              },
              child: const Text('应用到表格'),
            ),
          ],
        ),
      ),
    );
    // Dispose after the dialog's closing animation has released its fields.
    await Future<void>.delayed(const Duration(milliseconds: 300));
    first.dispose();
    last.dispose();
    for (final f in fields.values) {
      f.dispose();
    }
    if (result == true && mounted) setState(() => dirty = true);
  }

  Future<void> csv(bool importing) => run(() async {
    if (widget.textCsv) {
      final text = TextEditingController(
        text: importing ? '' : rewardsToCsv(rows),
      );
      final accepted = await showDialog<bool>(
        context: context,
        builder: (c) => AlertDialog(
          title: Text(importing ? '粘贴 CSV 内容' : '复制 CSV 内容'),
          content: SizedBox(
            width: 600,
            height: 300,
            child: TextField(
              controller: text,
              expands: true,
              maxLines: null,
              readOnly: !importing,
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(c, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(c, true),
              child: Text(importing ? '导入草稿' : '复制'),
            ),
          ],
        ),
      );
      final value = text.text;
      await Future<void>.delayed(const Duration(milliseconds: 300));
      text.dispose();
      if (accepted != true || !mounted) return;
      if (importing) {
        final next = rewardsFromCsv(value);
        if (await confirm('覆盖当前奖励草稿？', '导入后仍需点击保存才会写入线上服务器。') && mounted) {
          setState(() {
            rows = next;
            dirty = true;
            status = '已导入，尚未保存';
          });
        }
      } else {
        await Clipboard.setData(ClipboardData(text: value));
        if (mounted) setState(() => status = 'CSV 已复制');
      }
      return;
    }
    final path = TextEditingController(
      text: '${File(Platform.resolvedExecutable).parent.path}\\战斗奖励.csv',
    );
    final selected = await showDialog<String>(
      context: context,
      builder: (c) => AlertDialog(
        title: Text(importing ? '导入 CSV' : '导出 CSV'),
        content: SizedBox(
          width: 620,
          child: TextField(
            controller: path,
            decoration: const InputDecoration(
              labelText: 'CSV 文件完整路径',
              helperText: '可粘贴 Excel 文件保存路径；CSV 不改变自动升级开关',
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(c, path.text.trim()),
            child: const Text('继续'),
          ),
        ],
      ),
    );
    await Future<void>.delayed(const Duration(milliseconds: 300));
    path.dispose();
    if (selected == null || selected.isEmpty) return;
    final file = File(selected);
    if (importing) {
      final next = rewardsFromCsv(await file.readAsString());
      if (!mounted) return;
      final changes = rewardDiff({...rules, 'levels': next}, rules);
      if (await confirm(
        '导入 200 行到当前表格',
        '${changes.length} 项变化。确认后仍需点击保存。\n${changes.join('\n')}',
      )) {
        setState(() {
          rows = next;
          dirty = true;
          status = '已导入，尚未保存';
        });
      }
    } else {
      if (await file.exists()) {
        if (!mounted || !await confirm('覆盖 CSV 文件？', selected)) return;
      }
      await file.writeAsString(rewardsToCsv(rows));
      if (mounted) setState(() => status = '已导出：$selected');
    }
  });
  Future<void> copyOnline() => run(() async {
    final api = widget.environmentApi!;
    final local = await api({
      'operation': 'rewards_get',
      'environment': 'local',
    });
    final online = await api({
      'operation': 'rewards_get',
      'environment': 'online',
    });
    final source = Map<String, dynamic>.from(local['rules']);
    validateRewardRows(
      rewardRows(source),
      growth: source['growth_enabled'] == true,
    );
    final diff = rewardDiff(source, Map<String, dynamic>.from(online['rules']));
    if (!mounted) return;
    if (diff.isEmpty) {
      setState(() => status = '本地已保存配置与线上一致，无需复制');
      return;
    }
    if (!await confirm(
      '确认用本地已保存配置覆盖线上？',
      '共 ${diff.length} 项变化（线上旧值 → 本地新值）。不包含本页未保存修改。只复制奖励规则，请确认两边同编号物品定义一致；物品定义不会自动复制。\n${diff.join('\n')}',
    )) {
      return;
    }
    await api({
      'operation': 'rewards_save',
      'environment': 'online',
      'reward_revision': online['revision'],
      'rewards': source,
    });
    if (mounted) setState(() => status = '已复制到线上。线上页面请重新读取；并发修改会拒绝覆盖。');
  });
  @override
  Widget build(BuildContext context) => PopScope(
    canPop: !dirty,
    onPopInvokedWithResult: (didPop, result) async {
      if (!didPop && await confirm('放弃未保存修改？', '返回将丢弃本页修改。')) {
        if (mounted) {
          setState(() => dirty = false);
          if (context.mounted) Navigator.pop(context);
        }
      }
    },
    child: Scaffold(
      appBar: AppBar(title: Text('${widget.environment} · 战斗奖励')),
      body: Padding(
        padding: const EdgeInsets.all(20),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text(
              '1–200 级成长与战斗奖励表 · 点击行编辑。奖励按开战等级计算；200 级为满级。\n这是自定义规则；客户端经验条分母仍取客户端配置，修改曲线后显示可能不同。升级礼包、武器掉落可单独配置；空配置不发放。CSV仅包含成长数值，保留礼包和掉落。',
            ),
            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              title: const Text('启用自动升级（需填满 1–199 级升级经验）'),
              value: growth,
              onChanged: busy || rows.isEmpty
                  ? null
                  : (v) => setState(() {
                      growth = v;
                      dirty = true;
                    }),
            ),
            Wrap(
              spacing: 10,
              runSpacing: 8,
              children: [
                OutlinedButton(
                  onPressed: busy || revision == null
                      ? null
                      : () async {
                          final result =
                              await showDialog<List<Map<String, dynamic>>>(
                                context: context,
                                builder: (_) => DropConfigDialog(
                                  drops: drops,
                                  api: widget.api,
                                ),
                              );
                          if (result != null && mounted) {
                            setState(() {
                              drops = result;
                              dirty = true;
                            });
                          }
                        },
                  child: Text('武器掉落（${drops.length}条）'),
                ),
                OutlinedButton(
                  onPressed: busy || revision == null
                      ? null
                      : () async {
                          final result =
                              await showDialog<List<Map<String, dynamic>>>(
                                context: context,
                                builder: (_) => LevelGiftDialog(
                                  gifts: levelGifts,
                                  api: widget.api,
                                ),
                              );
                          if (result != null && mounted) {
                            setState(() {
                              levelGifts = result;
                              dirty = true;
                            });
                          }
                        },
                  child: Text('升级礼包（${levelGifts.length}级）'),
                ),
                OutlinedButton(
                  onPressed: busy || revision == null
                      ? null
                      : () async {
                          final result =
                              await showDialog<List<Map<String, dynamic>>>(
                                context: context,
                                builder: (_) => LevelGiftDialog(
                                  gifts: [tutorial],
                                  api: widget.api,
                                  tutorial: true,
                                ),
                              );
                          if (result != null && mounted)
                            setState(() {
                              tutorial = result.single;
                              dirty = true;
                            });
                        },
                  child: const Text('新手引导奖励'),
                ),
                OutlinedButton(
                  onPressed: busy || revision == null
                      ? null
                      : () async {
                          final result =
                              await showDialog<List<Map<String, dynamic>>>(
                                context: context,
                                builder: (_) => StageRewardDialog(
                                  rows: stageRewards,
                                  api: widget.api,
                                ),
                              );
                          if (result != null && mounted)
                            setState(() {
                              stageRewards = result;
                              dirty = true;
                            });
                        },
                  child: Text('关卡奖励（${stageRewards.length}张地图）'),
                ),

                OutlinedButton(
                  onPressed: busy
                      ? null
                      : () => Navigator.push(
                          context,
                          MaterialPageRoute<void>(
                            builder: (_) => ItemDefinitionsPage(
                              api: widget.api,
                              environment: widget.environment,
                            ),
                          ),
                        ),
                  child: const Text('奖励物品定义'),
                ),
                FilledButton(
                  onPressed: busy || revision == null || revision == 0
                      ? null
                      : save,
                  child: const Text('保存奖励配置'),
                ),
                OutlinedButton(
                  onPressed: busy ? null : load,
                  child: const Text('重新读取'),
                ),
                OutlinedButton(
                  onPressed: busy || rows.isEmpty ? null : () => edit(),
                  child: const Text('批量修改'),
                ),
                OutlinedButton(
                  onPressed: busy || rows.isEmpty ? null : () => csv(true),
                  child: const Text('导入 CSV'),
                ),
                OutlinedButton(
                  onPressed: busy || rows.isEmpty ? null : () => csv(false),
                  child: const Text('导出 CSV'),
                ),
                OutlinedButton(
                  onPressed: busy || widget.environmentApi == null
                      ? null
                      : copyOnline,
                  child: const Text('本地复制到线上（预览差异）'),
                ),
              ],
            ),
            if (busy) const LinearProgressIndicator(),
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 8),
              child: SelectableText('${dirty ? '有未保存修改 · ' : ''}$status'),
            ),
            Expanded(
              child: SingleChildScrollView(
                scrollDirection: Axis.horizontal,
                child: SizedBox(
                  width: 1060,
                  child: Column(
                    children: [
                      Row(
                        children: [
                          for (final label in rewardColumns.values)
                            Expanded(
                              child: Padding(
                                padding: const EdgeInsets.all(8),
                                child: Text(
                                  label,
                                  style: const TextStyle(
                                    fontWeight: FontWeight.bold,
                                  ),
                                ),
                              ),
                            ),
                        ],
                      ),
                      Expanded(
                        child: ListView.builder(
                          itemCount: rows.length,
                          itemBuilder: (c, i) => InkWell(
                            onTap: busy ? null : () => edit(index: i),
                            child: ColoredBox(
                              color: i.isEven
                                  ? Colors.blue.withValues(alpha: 0.04)
                                  : Colors.transparent,
                              child: Padding(
                                padding: const EdgeInsets.symmetric(
                                  vertical: 10,
                                ),
                                child: Row(
                                  children: [
                                    for (final k in rewardColumns.keys)
                                      Expanded(
                                        child: Padding(
                                          padding: const EdgeInsets.symmetric(
                                            horizontal: 8,
                                          ),
                                          child: Text(
                                            '${rows[i][k]}',
                                          ),
                                        ),
                                      ),
                                  ],
                                ),
                              ),
                            ),
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    ),
  );
}
