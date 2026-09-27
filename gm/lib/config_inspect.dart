import 'package:flutter/material.dart';
import 'package:file_selector/file_selector.dart';

List<String> compareLevelCosts(List<dynamic> client, List<dynamic> server) {
  final a = {for (final r in client) r['level']: r['next_experience']};
  final b = {for (final r in server) r['level']: r['next_experience']};
  final levels = {...a.keys, ...b.keys}.toList()
    ..sort((x, y) => (x as num).compareTo(y as num));
  return [
    for (final n in levels)
      if (a[n] != b[n]) '$n 级：客户端 ${a[n] ?? "缺失"}，服务器 ${b[n] ?? "缺失"}',
  ];
}

class ConfigInspectPage extends StatefulWidget {
  const ConfigInspectPage({
    super.key,
    required this.api,
    required this.environment,
  });
  final Future<dynamic> Function(Map<String, dynamic>) api;
  final String environment;
  @override
  State<ConfigInspectPage> createState() => _ConfigInspectPageState();
}

class _ConfigInspectPageState extends State<ConfigInspectPage> {
  final path = TextEditingController(), search = TextEditingController();
  String loaded = '',
      revision = '',
      category = '全部',
      content = '',
      selected = '',
      status = '';
  List<dynamic> files = [], levels = [];
  bool busy = false;
  @override
  void dispose() {
    path.dispose();
    search.dispose();
    super.dispose();
  }

  Future<void> run(Future<void> Function() f) async {
    if (busy) return;
    setState(() => busy = true);
    try {
      await f();
    } catch (e) {
      if (mounted) setState(() => status = '$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<dynamic> call(String op, {String file = ''}) => widget.api({
    'operation': 'client_config_$op',
    'client_config': {'base': loaded, 'revision': revision, 'file': file},
  });
  Future<void> load() async {
    final r = await widget.api({
      'operation': 'client_config_inspect',
      'client_config': {'base': path.text.trim()},
    });
    if (!mounted) return;
    setState(() {
      loaded = r['base'];
      revision = r['revision'];
      files = r['files'];
      levels = r['levels'] ?? [];
      selected = '';
      content = '';
      status = '已校验并解析 ${files.length} 个文件';
      category = '全部';
    });
  }

  @override
  Widget build(BuildContext context) {
    final names = ['全部', ...files.map((f) => f['category'] as String).toSet()];
    final visible = files
        .where(
          (f) =>
              (category == '全部' || category == f['category']) &&
              f['name'].toString().toLowerCase().contains(
                search.text.toLowerCase(),
              ),
        )
        .toList();
    return Scaffold(
      appBar: AppBar(title: const Text('配置解析')),
      body: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const Text('只读查看与导出，不修改游戏文件。武器、地图编辑仍在「客户端配置」；商城价格、奖励仍由服务器管理。'),
            const SizedBox(height: 12),
            Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: path,
                    enabled: !busy,
                    decoration: const InputDecoration(
                      labelText: 'config.spf2 完整路径',
                      border: OutlineInputBorder(),
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                OutlinedButton(
                  onPressed: busy
                      ? null
                      : () => run(() async {
                          final f = await openFile(
                            acceptedTypeGroups: [
                              const XTypeGroup(
                                label: '配置包',
                                extensions: ['spf2'],
                              ),
                            ],
                          );
                          if (f != null && mounted) {
                            path.text = f.path;
                            await load();
                          }
                        }),
                  child: const Text('选择文件'),
                ),
                const SizedBox(width: 8),
                FilledButton(
                  onPressed: busy ? null : () => run(load),
                  child: const Text('解析'),
                ),
              ],
            ),
            Wrap(
              spacing: 12,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                DropdownButton<String>(
                  value: category,
                  items: names
                      .map((n) => DropdownMenuItem(value: n, child: Text(n)))
                      .toList(),
                  onChanged: busy ? null : (v) => setState(() => category = v!),
                ),
                SizedBox(
                  width: 230,
                  child: TextField(
                    controller: search,
                    onChanged: (_) => setState(() {}),
                    decoration: const InputDecoration(labelText: '搜索文件名'),
                  ),
                ),
                OutlinedButton(
                  onPressed: busy || loaded.isEmpty
                      ? null
                      : () => run(() async {
                          final r = await call('extract');
                          if (mounted) {
                            setState(
                              () => status =
                                  '已导出 ${r['count']} 个文件：${r['folder']}',
                            );
                          }
                        }),
                  child: const Text('全部导出到独立文件夹'),
                ),
                OutlinedButton(
                  onPressed: busy || levels.isEmpty
                      ? null
                      : () => run(() async {
                          // Revalidate the selected file before comparing to the live environment.
                          await call('inspect', file: 'levelup.txt');
                          final r = await widget.api({
                            'operation': 'rewards_get',
                          });
                          final differences = compareLevelCosts(
                            levels,
                            r['rules']['levels'] as List? ?? [],
                          );
                          if (!mounted) return;
                          setState(() {
                            selected = '经验门槛核对 · ${widget.environment}';
                            content = differences.isEmpty
                                ? '客户端与${widget.environment}的等级经验门槛一致。'
                                : '发现 ${differences.length} 处差异：\n${differences.join('\n')}';
                            content += '\n\n此处只核对，不自动覆盖任意一方。服务器经验配置控制实际升级与奖励，levelup.txt 控制客户端经验显示。';
                          });
                        }),
                  child: Text('对比${widget.environment}经验门槛'),
                ),
              ],
            ),
            if (busy) const LinearProgressIndicator(),
            SelectableText(status),
            if (loaded.isNotEmpty)
              SelectableText(
                '来源：$loaded\nSHA-256：$revision',
                style: Theme.of(context).textTheme.bodySmall,
              ),
            const Divider(),
            Expanded(
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  SizedBox(
                    width: 280,
                    child: ListView.builder(
                      itemCount: visible.length,
                      itemBuilder: (context, i) {
                        final f = visible[i];
                        return ListTile(
                          dense: true,
                          selected: selected == f['name'],
                          title: Text(f['name']),
                          subtitle: Text('${f['category']} · ${f['size']} 字节'),
                          onTap: busy
                              ? null
                              : () => run(() async {
                                  final r = await call(
                                    'inspect',
                                    file: f['name'],
                                  );
                                  if (mounted) {
                                    setState(() {
                                      selected = f['name'];
                                      content = r['content'];
                                    });
                                  }
                                }),
                        );
                      },
                    ),
                  ),
                  const VerticalDivider(),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(selected),
                        const SizedBox(height: 8),
                        Expanded(
                          child: SingleChildScrollView(
                            child: SelectableText(content),
                          ),
                        ),
                      ],
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
