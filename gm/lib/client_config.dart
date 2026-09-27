import 'package:flutter/material.dart';

import 'weapon_config.dart';

import 'package:file_selector/file_selector.dart';

import 'item_pictures.dart';

class ClientConfigPage extends StatefulWidget {
  const ClientConfigPage({super.key, required this.api});
  final Future<dynamic> Function(Map<String, dynamic>) api;
  @override
  State<ClientConfigPage> createState() => _ClientConfigPageState();
}

class _ClientConfigPageState extends State<ClientConfigPage> {
  final base = TextEditingController(),
      resourceRoot = TextEditingController(),
      search = TextEditingController(),
      name = TextEditingController(),
      content = TextEditingController(),
      resources = TextEditingController(),
      version = TextEditingController(
        text:
            '${DateTime.now().year}.${DateTime.now().month.toString().padLeft(2, '0')}.${DateTime.now().day.toString().padLeft(2, '0')}-config.${DateTime.now().millisecondsSinceEpoch}',
      ),
      notes = TextEditingController();
  final fields = <String, TextEditingController>{};
  final effectRows = <Map<String, String>>[];
  String category = 'weapons', revision = '', message = '', folder = '';
  String? file, preview;
  bool busy = false, advanced = false;
  List<dynamic> files = [], records = [], plans = [];
  Map<String, dynamic>? record, report;
  final selected = <String>{};
  late final pictures = ItemPictures(widget.api);
  static const categories = {
    'weapons': '武器',
    'effects': '特效',
    'maps': '地图',
    'items': '装备与道具',
    'package': '打包发布',
  };
  static const labels = {
    'MusicChannel': '音乐频道',
    'Name': '名称',
    'MapId': '地图编号',
    'ItemID': '武器编号',
    'worldpath': '地图资源目录',
    'xmlfile': '场景配置',
    'PicName': '预览图片',
    'Intro': '介绍',
    'MaxPlayer': '最大人数',
    'Difficulty': '难度',
    'Priority': '排序',
    'File': '资源文件',
    'EffectId': '特效编号',
  };
  @override
  void initState() {
    super.initState();
    loadPlans();
  }

  @override
  void dispose() {
    for (final c in [
      base,
      resourceRoot,
      search,
      name,
      content,
      resources,
      version,
      notes,
      ...fields.values,
    ]) {
      c.dispose();
    }
    super.dispose();
  }

  Map<String, dynamic> request() => {
    'base': base.text.trim(),
    'resource_root': resourceRoot.text.trim(),
    'category': category,
    'file': file,
    'revision': revision,
    'selected': selected.toList(),
    'resources': resources.text
        .split('\n')
        .map((e) => e.trim())
        .where((e) => e.isNotEmpty)
        .toList(),
    'version': version.text.trim(),
    'notes': notes.text,
  };
  Future<dynamic> call(String operation, [Map<String, dynamic>? extra]) =>
      widget.api({
        'operation': 'client_config_$operation',
        'client_config': {...request(), ...?extra},
      });
  Future<void> task(Future<void> Function() work) async {
    if (busy) return;
    setState(() => busy = true);
    try {
      await work();
    } catch (e) {
      if (mounted) setState(() => message = '$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> loadPlans() async {
    try {
      final r = await call('plans');
      if (mounted)
        setState(() {
          plans = r['plans'];
          folder = r['folder'];
          if (base.text.isEmpty) {
            base.text = r['base'] ?? '';
            resourceRoot.text = r['resource_root'] ?? '';
          }
        });
    } catch (e) {
      if (mounted) setState(() => message = '$e');
    }
  }

  void invalidate() {
    preview = null;
    report = null;
  }

  Future<void> loadCatalog() async {
    final r = await call('catalog');
    if (!mounted) return;
    setState(() {
      files = r['files'];
      revision = r['revision'];
      plans = r['plans'];
      folder = r['folder'];
      if (base.text.isEmpty) {
        base.text = r['base'] ?? '';
        resourceRoot.text = r['resource_root'] ?? '';
      }
      file = null;
      record = null;
      records = [];
      invalidate();
      message = '已加载基础配置，编辑只保存为方案';
    });
  }

  Future<void> loadRecords(String value) async {
    file = value;
    final r = await call('records');
    if (!mounted) return;
    setState(() {
      records = r['records'];
      revision = r['revision'];
      record = null;
      search.clear();
    });
  }

  void chooseRecord(Map<String, dynamic> next) {
    for (final c in fields.values) {
      c.dispose();
    }
    fields.clear();
    for (final entry in (next['values'] as Map? ?? {}).entries) {
      fields['${entry.key}'] = TextEditingController(text: '${entry.value}');
    }
    effectRows.clear();
    for (final row in next['effect_rows'] as List? ?? []) {
      effectRows.add(Map<String, String>.from(row));
    }
    setState(() {
      record = next;
      name.text = '${categories[category]} · ${next['label']}';
      content.text = next['content'];
      advanced = false;
    });
  }

  Future<void> savePlan() async {
    final r = await call('save', {
      'name': name.text.trim(),
      'record': record!['key'],
      'content': content.text,
      if (!advanced) 'values': fields.map((k, v) => MapEntry(k, v.text)),
      if (!advanced && file == 'acteffect.xml') 'effect_rows': effectRows,
    });
    if (!mounted) return;
    setState(() {
      message = r['message'];
      invalidate();
    });
    await loadPlans();
  }

  Widget input(
    TextEditingController c,
    String label, {
    int lines = 1,
    void Function(String)? onChanged,
    bool enabled = true,
  }) => Padding(
    padding: const EdgeInsets.only(bottom: 10),
    child: TextField(
      controller: c,
      enabled: enabled && !busy,
      maxLines: lines,
      decoration: InputDecoration(
        labelText: label,
        border: const OutlineInputBorder(),
      ),
      onChanged: onChanged,
    ),
  );
  Widget planList() => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      const Text(
        '选择要合入的方案',
        style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
      ),
      if (plans.isEmpty)
        const Padding(
          padding: EdgeInsets.all(20),
          child: Text('还没有方案，请先在分类中保存修改。'),
        ),
      for (final p in plans)
        CheckboxListTile(
          value: selected.contains(p['id']),
          title: Text('${p['name']}'),
          subtitle: Text('${categories[p['category']]} · ${p['id']}'),
          onChanged: busy
              ? null
              : (v) => setState(() {
                  v == true ? selected.add(p['id']) : selected.remove(p['id']);
                  invalidate();
                }),
        ),
    ],
  );
  @override
  Widget build(BuildContext context) {
    final filtered = records
        .where(
          (r) =>
              '${r['label']}'.toLowerCase().contains(search.text.toLowerCase()),
        )
        .toList();
    return Scaffold(
      appBar: AppBar(
        title: const Text('客户端配置'),
        actions: [
          IconButton(
            onPressed: busy ? null : () => task(loadPlans),
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      body: Column(
        children: [
          if (busy) const LinearProgressIndicator(),
          Padding(
            padding: const EdgeInsets.all(12),
            child: Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                for (final e in categories.entries)
                  ChoiceChip(
                    label: Text(e.value),
                    selected: category == e.key,
                    onSelected: busy
                        ? null
                        : (_) => setState(() {
                            category = e.key;
                            files = [];
                            records = [];
                            record = null;
                            file = null;
                            message = '';
                          }),
                  ),
              ],
            ),
          ),
          Expanded(
            child: ListView(
              padding: const EdgeInsets.fromLTRB(20, 0, 20, 24),
              children: [
                Wrap(
                  spacing: 8,
                  children: [
                    TextButton.icon(
                      onPressed: busy
                          ? null
                          : () async {
                              final f = await openFile(
                                acceptedTypeGroups: [
                                  const XTypeGroup(
                                    label: '客户端配置',
                                    extensions: ['spf2'],
                                  ),
                                ],
                              );
                              if (f != null && mounted)
                                setState(() {
                                  base.text = f.path;
                                  revision = '';
                                  files = [];
                                  records = [];
                                  record = null;
                                  invalidate();
                                });
                            },
                      icon: const Icon(Icons.file_open),
                      label: const Text('选择基础配置'),
                    ),
                    TextButton.icon(
                      onPressed: busy
                          ? null
                          : () async {
                              final d = await getDirectoryPath();
                              if (d != null && mounted)
                                setState(() {
                                  resourceRoot.text = d;
                                  invalidate();
                                });
                            },
                      icon: const Icon(Icons.folder_open),
                      label: const Text('选择资源目录'),
                    ),
                  ],
                ),
                input(
                  base,
                  '基础 config.spf2 完整路径',
                  onChanged: (_) => setState(() {
                    revision = '';
                    file = null;
                    record = null;
                    files = [];
                    records = [];
                    invalidate();
                  }),
                ),
                input(
                  resourceRoot,
                  '资源所在的游戏目录（用于查找模型、贴图等）',
                  onChanged: (_) => setState(invalidate),
                ),
                if (category != 'package')
                  Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: [
                      FilledButton(
                        onPressed: busy ? null : () => task(loadCatalog),
                        child: const Text('读取配置'),
                      ),
                      if (category == 'weapons')
                        OutlinedButton(
                          onPressed: busy
                              ? null
                              : () async {
                                  await Navigator.push(
                                    context,
                                    MaterialPageRoute<void>(
                                      builder: (_) =>
                                          WeaponConfigPage(api: widget.api),
                                    ),
                                  );
                                  await loadPlans();
                                },
                          child: const Text('招式、伤害、受击与 BUFF 编辑'),
                        ),
                      if (category == 'weapons')
                        OutlinedButton(
                          onPressed: busy
                              ? null
                              : () => task(() async {
                                  final r = await call('import_weapons');
                                  setState(() => message = r['message']);
                                  await loadPlans();
                                }),
                          child: const Text('导入以前保存的武器方案'),
                        ),
                    ],
                  ),
                const SizedBox(height: 12),
                if (category == 'package') ...[
                  const Text('先预览变化和资源，再生成 OSS 上传包。不会上传或修改线上服务器。'),
                  const SizedBox(height: 12),
                  planList(),
                  const SizedBox(height: 12),
                  input(
                    version,
                    '新版本号',
                    onChanged: (_) => setState(invalidate),
                  ),
                  input(
                    notes,
                    '更新说明（可留空）',
                    lines: 3,
                    onChanged: (_) => setState(invalidate),
                  ),
                  input(
                    resources,
                    '附加资源：每行一个 Data/ 相对文件或目录',
                    lines: 3,
                    onChanged: (_) => setState(invalidate),
                  ),
                  Wrap(
                    spacing: 8,
                    children: [
                      FilledButton(
                        onPressed: busy
                            ? null
                            : () => task(() async {
                                final r = Map<String, dynamic>.from(
                                  await call('preview'),
                                );
                                setState(() {
                                  report = r;
                                  preview = r['preview'];
                                  message = r['message'];
                                });
                              }),
                        child: const Text('预览合并并检查'),
                      ),
                      FilledButton(
                        onPressed: busy || preview == null
                            ? null
                            : () => task(() async {
                                final r = Map<String, dynamic>.from(
                                  await call('build', {'preview': preview}),
                                );
                                setState(() {
                                  report = r;
                                  message = '${r['message']}\n${r['zip']}';
                                });
                              }),
                        child: const Text('生成 OSS 更新包'),
                      ),
                    ],
                  ),
                  if (report != null) ...[
                    const SizedBox(height: 12),
                    for (final change in report!['changes'] as List? ?? [])
                      Text('• $change'),
                    ExpansionTile(
                      title: Text(
                        '打包文件 ${(report!['files'] as Map?)?.length ?? 0} 项',
                      ),
                      children: [
                        for (final path
                            in (report!['files'] as Map? ?? {}).keys)
                          ListTile(dense: true, title: SelectableText('$path')),
                      ],
                    ),
                    if (report!['zip'] != null)
                      SelectableText('${report!['zip']}'),
                  ],
                ] else ...[
                  if (files.isNotEmpty)
                    DropdownButtonFormField<String>(
                      initialValue: file,
                      key: ValueKey('$category/$revision'),
                      isExpanded: true,
                      decoration: const InputDecoration(labelText: '配置文件'),
                      items: [
                        for (final f in files)
                          DropdownMenuItem(value: '$f', child: Text('$f')),
                      ],
                      onChanged: busy
                          ? null
                          : (v) {
                              if (v != null) task(() => loadRecords(v));
                            },
                    ),
                  if (records.isNotEmpty) ...[
                    const SizedBox(height: 12),
                    input(search, '搜索名称或编号', onChanged: (_) => setState(() {})),
                    SizedBox(
                      height: 220,
                      child: ListView.builder(
                        itemCount: filtered.length,
                        itemBuilder: (c, i) {
                          final r = filtered[i];
                          return ListTile(
                            leading: file == 'item.txt'
                                ? pictures.preview({
                                    'key': r['key'],
                                    'id': int.tryParse(
                                      '${r['key']}'.split(':').last,
                                    ),
                                  })
                                : null,
                            selected: record?['key'] == r['key'],
                            title: Text('${r['label']}'),
                            onTap: busy
                                ? null
                                : () => chooseRecord(
                                    Map<String, dynamic>.from(r),
                                  ),
                          );
                        },
                      ),
                    ),
                  ],
                  if (record != null) ...[
                    const Divider(),
                    input(name, '方案名称'),
                    for (final entry in fields.entries)
                      input(
                        entry.value,
                        labels[entry.key] ?? entry.key,
                        enabled: ![
                          'MapId',
                          'ItemID',
                          'Id',
                          'id',
                          'ID',
                          'Level',
                          'Mode',
                          'Key',
                        ].contains(entry.key),
                      ),
                    if (file == 'acteffect.xml') ...[
                      const Text('武器加载的特效'),
                      for (int i = 0; i < effectRows.length; i++)
                        Padding(
                          padding: const EdgeInsets.symmetric(vertical: 4),
                          child: Row(
                            children: [
                              Expanded(
                                child: TextFormField(
                                  key: ValueKey(
                                    '${identityHashCode(effectRows[i])}-id',
                                  ),
                                  initialValue: effectRows[i]['EffectId'],
                                  decoration: const InputDecoration(
                                    labelText: '特效编号',
                                  ),
                                  onChanged: (v) =>
                                      effectRows[i]['EffectId'] = v,
                                ),
                              ),
                              const SizedBox(width: 8),
                              Expanded(
                                child: TextFormField(
                                  key: ValueKey(
                                    '${identityHashCode(effectRows[i])}-file',
                                  ),
                                  initialValue: effectRows[i]['File'],
                                  decoration: const InputDecoration(
                                    labelText: '资源文件',
                                  ),
                                  onChanged: (v) => effectRows[i]['File'] = v,
                                ),
                              ),
                              IconButton(
                                onPressed: busy
                                    ? null
                                    : () => setState(
                                        () => effectRows.removeAt(i),
                                      ),
                                icon: const Icon(Icons.remove_circle_outline),
                              ),
                            ],
                          ),
                        ),
                      TextButton.icon(
                        onPressed: busy
                            ? null
                            : () => setState(
                                () => effectRows.add({
                                  'EffectId': '',
                                  'File': '',
                                }),
                              ),
                        icon: const Icon(Icons.add),
                        label: const Text('添加特效绑定'),
                      ),
                    ],
                    ExpansionTile(
                      title: const Text('高级：使用完整记录替代上方字段'),
                      onExpansionChanged: (v) => setState(() => advanced = v),
                      children: [
                        const Text('展开时只保存下方完整记录，上方字段的修改不参与保存。'),
                        input(content, '当前记录内容', lines: 12),
                      ],
                    ),
                    input(resources, '随方案附带的资源：每行 Data/ 相对文件或目录', lines: 3),
                    FilledButton(
                      onPressed: busy ? null : () => task(savePlan),
                      child: const Text('保存为独立方案'),
                    ),
                  ],
                ],
                const SizedBox(height: 16),
                if (message.isNotEmpty) SelectableText(message),
                if (folder.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.only(top: 12),
                    child: SelectableText('方案目录：$folder'),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
