import 'package:flutter/material.dart';

import 'weapon_config.dart';
import 'buff_config.dart';
import 'weapon_merge_page.dart';

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
  bool busy = false, advanced = false, dirty = false;
  List<dynamic> files = [], records = [], plans = [];
  Map<String, dynamic>? record, report;
  final selected = <String>{};
  late final pictures = ItemPictures(widget.api);
  static const categories = {
    'weapons': '武器',
    'effects': '特效',
    'maps': '地图',
    'items': '装备与道具',
    'buffs': '状态/Buff',
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
    for (final c in [name, content, resources]) {
      c.addListener(markDirty);
    }
    task(() async {
      await loadPlans();
      if (base.text.isNotEmpty) await loadCatalog();
    });
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

  void markDirty() {
    if (record != null && !dirty && mounted) setState(() => dirty = true);
  }

  Future<bool> confirmChanges() async {
    if (!dirty) return true;
    final action = await showDialog<String>(
      context: context,
      builder: (c) => AlertDialog(
        title: const Text('有未保存的配置修改'),
        content: const Text('保存为独立方案后可在打包发布时合入，基础配置不会被覆盖。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c),
            child: const Text('继续编辑'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(c, 'discard'),
            child: const Text('丢弃修改'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(c, 'save'),
            child: const Text('保存后继续'),
          ),
        ],
      ),
    );
    if (!mounted || action == null) return false;
    if (action == 'save') {
      await task(savePlan);
      return !dirty;
    }
    setState(() => dirty = false);
    return true;
  }

  Future<void> selectCategory(String value) async {
    if (value == category || !await confirmChanges() || !mounted) return;
    setState(() {
      category = value;
      files = [];
      records = [];
      record = null;
      file = null;
      message = '';
    });
    if (value != 'package' && base.text.isNotEmpty) await task(loadCatalog);
  }

  Future<void> chooseBase() async {
    if (!await confirmChanges() || !mounted) return;
    final f = await openFile(
      acceptedTypeGroups: [
        const XTypeGroup(label: '客户端配置', extensions: ['spf2']),
      ],
    );
    if (f == null || !mounted) return;
    setState(() {
      base.text = f.path;
      revision = '';
      record = null;
      file = null;
      files = [];
      records = [];
      invalidate();
    });
    if (category != 'package') await task(loadCatalog);
  }

  Future<void> leave() async {
    if (busy || !await confirmChanges() || !mounted) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) Navigator.pop(context);
    });
  }

  Future<void> openWeaponEditor() async {
    if (!await confirmChanges() || !mounted) return;
    await Navigator.push(
      context,
      MaterialPageRoute<void>(
        builder: (_) => WeaponConfigPage(api: widget.api),
      ),
    );
    if (mounted) await loadPlans();
  }

  Future<void> openWeaponMerge() async {
    if (!await confirmChanges() || !mounted) return;
    await Navigator.push(
      context,
      MaterialPageRoute<bool>(
        builder: (_) => WeaponMergePage(
          api: widget.api,
          clientConfig: {
            'base': base.text.trim(),
            'resource_root': resourceRoot.text.trim(),
          },
        ),
      ),
    );
    if (mounted) await loadPlans();
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
    if (files.isNotEmpty) {
      final preferred = category == 'effects'
          ? 'acteffect.xml'
          : category == 'maps'
          ? 'mapmgr.xml'
          : 'item.txt';
      await loadRecords(
        files.contains(preferred) ? preferred : '${files.first}',
      );
    }
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
    record = null;
    for (final c in fields.values) {
      c.dispose();
    }
    fields.clear();
    for (final entry in (next['values'] as Map? ?? {}).entries) {
      fields['${entry.key}'] = TextEditingController(text: '${entry.value}')
        ..addListener(markDirty);
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
      dirty = false;
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
      dirty = false;
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
      enabled: !busy,
      readOnly: !enabled,
      maxLines: lines,
      decoration: InputDecoration(
        labelText: label,
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(10),
          borderSide: const BorderSide(color: Color(0xffdce5eb)),
        ),
      ),
      onChanged: onChanged,
    ),
  );
  Widget planList() => SizedBox(
    height: plans.isEmpty ? 90 : 230,
    child: plans.isEmpty
        ? const Center(child: Text('还没有方案，请先在分类中保存修改。'))
        : ListView.builder(
            itemCount: plans.length,
            itemBuilder: (_, i) {
              final p = plans[i];
              return CheckboxListTile(
                value: selected.contains(p['id']),
                title: Text('${p['name']}'),
                subtitle: Text('${categories[p['category']]} · ${p['id']}'),
                onChanged: busy
                    ? null
                    : (v) => setState(() {
                        v == true
                            ? selected.add(p['id'])
                            : selected.remove(p['id']);
                        invalidate();
                      }),
              );
            },
          ),
  );
  static const descriptions = {
    'weapons': '武器定义、动作绑定及独立修改方案',
    'buffs': '状态效果、属性及 Buff 定制',
    'effects': '特效资源和武器加载绑定',
    'maps': '场景、音乐、封面及地图校验',
    'items': '头饰、背饰、套装、称号与道具定义',
    'package': '选择方案，检查依赖，生成更新包',
  };
  static const icons = {
    'weapons': Icons.sports_martial_arts,
    'buffs': Icons.bolt_outlined,
    'effects': Icons.auto_awesome,
    'maps': Icons.landscape_outlined,
    'items': Icons.inventory_2_outlined,
    'package': Icons.archive_outlined,
  };
  Widget section(String title, Widget child) => Card(
    color: Colors.white,
    elevation: 0,
    shape: RoundedRectangleBorder(
      borderRadius: BorderRadius.circular(12),
      side: const BorderSide(color: Color(0xffe0e7ed)),
    ),
    margin: EdgeInsets.zero,
    child: Padding(
      padding: const EdgeInsets.all(18),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(title, style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 14),
          child,
        ],
      ),
    ),
  );
  Widget sourcePanel() => section(
    '配置来源',
    Column(
      children: [
        Row(
          children: [
            Expanded(child: input(base, '基础 config.spf2', enabled: false)),
            const SizedBox(width: 12),
            OutlinedButton.icon(
              onPressed: busy ? null : chooseBase,
              icon: const Icon(Icons.file_open),
              label: const Text('选择基础配置'),
            ),
          ],
        ),
        Row(
          children: [
            Expanded(
              child: input(
                resourceRoot,
                '资源目录（模型、贴图与音效）',
                onChanged: (_) => setState(invalidate),
              ),
            ),
            const SizedBox(width: 12),
            OutlinedButton.icon(
              onPressed: busy
                  ? null
                  : () async {
                      final path = await getDirectoryPath();
                      if (path != null && mounted)
                        setState(() {
                          resourceRoot.text = path;
                          invalidate();
                        });
                    },
              icon: const Icon(Icons.folder_open),
              label: const Text('选择资源目录'),
            ),
          ],
        ),
        Align(
          alignment: Alignment.centerLeft,
          child: Text(
            category == 'package'
                ? '只生成本地上传包；发布与服务器同步仍需单独操作。'
                : '修改先保存为独立方案，基础配置不会直接覆盖。',
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ),
      ],
    ),
  );
  Widget recordsPanel() {
    final filtered = records
        .where(
          (r) =>
              '${r['label']}'.toLowerCase().contains(search.text.toLowerCase()),
        )
        .toList();
    return Card(
      color: Colors.white,
      elevation: 0,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: const BorderSide(color: Color(0xffe0e7ed)),
      ),
      margin: EdgeInsets.zero,
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          children: [
            DropdownButtonFormField<String>(
              initialValue: file,
              key: ValueKey('$category/$revision'),
              isExpanded: true,
              decoration: const InputDecoration(
                labelText: '配置文件',
                border: OutlineInputBorder(),
              ),
              items: [
                for (final f in files)
                  DropdownMenuItem(value: '$f', child: Text('$f')),
              ],
              onChanged: busy
                  ? null
                  : (v) async {
                      if (v != null && await confirmChanges() && mounted)
                        await task(() => loadRecords(v));
                    },
            ),
            const SizedBox(height: 12),
            input(search, '搜索名称或编号', onChanged: (_) => setState(() {})),
            Align(
              alignment: Alignment.centerLeft,
              child: Text(
                '共 ${filtered.length} 条记录',
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ),
            const SizedBox(height: 8),
            Expanded(
              child: filtered.isEmpty
                  ? const Center(child: Text('请选择配置文件'))
                  : ListView.builder(
                      itemCount: filtered.length,
                      itemBuilder: (_, i) {
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
                              : () async {
                                  if (await confirmChanges() && mounted)
                                    chooseRecord(Map<String, dynamic>.from(r));
                                },
                        );
                      },
                    ),
            ),
          ],
        ),
      ),
    );
  }

  Widget editorPanel() => Card(
    color: Colors.white,
    elevation: 0,
    shape: RoundedRectangleBorder(
      borderRadius: BorderRadius.circular(12),
      side: const BorderSide(color: Color(0xffe0e7ed)),
    ),
    margin: EdgeInsets.zero,
    child: record == null
        ? const Center(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(Icons.edit_note, size: 52, color: Colors.blueGrey),
                SizedBox(height: 12),
                Text('从左侧选择记录开始编辑'),
                SizedBox(height: 6),
                Text('修改按类型独立保存，打包时再合并'),
              ],
            ),
          )
        : ListView(
            padding: const EdgeInsets.all(20),
            children: [
              Text(
                '${record!['label']}',
                style: Theme.of(context).textTheme.titleLarge,
              ),
              const SizedBox(height: 8),
              Text(
                dirty ? '有未保存的修改' : '独立方案编辑',
                style: TextStyle(
                  color: dirty ? Colors.deepOrange : Colors.blueGrey,
                ),
              ),
              const SizedBox(height: 20),
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
                            onChanged: (v) {
                              effectRows[i]['EffectId'] = v;
                              markDirty();
                            },
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
                            onChanged: (v) {
                              effectRows[i]['File'] = v;
                              markDirty();
                            },
                          ),
                        ),
                        IconButton(
                          onPressed: busy
                              ? null
                              : () => setState(() {
                                  effectRows.removeAt(i);
                                  dirty = true;
                                }),
                          icon: const Icon(Icons.remove_circle_outline),
                        ),
                      ],
                    ),
                  ),
                TextButton.icon(
                  onPressed: busy
                      ? null
                      : () => setState(() {
                          effectRows.add({'EffectId': '', 'File': ''});
                          dirty = true;
                        }),
                  icon: const Icon(Icons.add),
                  label: const Text('添加特效绑定'),
                ),
              ],
              ExpansionTile(
                title: const Text('高级：使用完整记录替代上方字段'),
                onExpansionChanged: (v) => setState(() {
                  advanced = v;
                  dirty = true;
                }),
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
          ),
  );
  Widget emptyPanel() => section(
    '开始管理${categories[category]}',
    Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Text('选择基础配置后读取记录。武器 ZIP 可先对比差异，再合并到临时配置。'),
        const SizedBox(height: 20),
        Wrap(
          spacing: 24,
          runSpacing: 16,
          children: [
            for (final text in ['① 读取基础配置', '② 编辑并保存独立方案', '③ 打包发布时检查合并'])
              Chip(label: Text(text)),
          ],
        ),
        const SizedBox(height: 20),
        if (plans.isNotEmpty) Text('已有 ${plans.length} 个独立方案，可在「打包发布」选择合入。'),
      ],
    ),
  );
  Widget toolbar() => Wrap(
    spacing: 10,
    runSpacing: 8,
    children: [
      FilledButton.icon(
        onPressed: busy
            ? null
            : () async {
                if (await confirmChanges() && mounted) await task(loadCatalog);
              },
        icon: const Icon(Icons.refresh),
        label: const Text('读取配置'),
      ),
      if (category == 'weapons') ...[
        OutlinedButton.icon(
          onPressed: busy ? null : openWeaponEditor,
          icon: const Icon(Icons.edit_note),
          label: const Text('招式、伤害、受击与 BUFF 编辑'),
        ),
        OutlinedButton.icon(
          onPressed: busy ? null : openWeaponMerge,
          icon: const Icon(Icons.call_merge),
          label: const Text('导入武器 ZIP'),
        ),
        TextButton(
          onPressed: busy
              ? null
              : () => task(() async {
                  final r = await call('import_weapons');
                  if (mounted) setState(() => message = r['message']);
                  await loadPlans();
                }),
          child: const Text('导入已保存的编辑方案'),
        ),
      ],
      if (category == 'buffs')
        OutlinedButton(
          onPressed: busy
              ? null
              : () async {
                  await Navigator.push(
                    context,
                    MaterialPageRoute<void>(
                      builder: (_) => BuffConfigPage(api: widget.api),
                    ),
                  );
                  await loadPlans();
                },
          child: const Text('打开状态/Buff 定制'),
        ),
    ],
  );
  @override
  Widget build(BuildContext context) => PopScope(
    canPop: !dirty && !busy,
    onPopInvokedWithResult: (didPop, _) {
      if (!didPop) leave();
    },
    child: Scaffold(
      appBar: AppBar(
        title: const Text('客户端配置'),
        leading: IconButton(
          onPressed: busy ? null : leave,
          icon: const Icon(Icons.arrow_back),
        ),
        actions: [
          IconButton(
            tooltip: '刷新方案',
            onPressed: busy ? null : () => task(loadPlans),
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      body: Column(
        children: [
          if (busy) const LinearProgressIndicator(),
          Expanded(
            child: LayoutBuilder(
              builder: (context, size) {
                final wide = size.maxWidth >= 1100;
                final navigation = Card(
                  color: Colors.white,
                  elevation: 0,
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(12),
                    side: const BorderSide(color: Color(0xffe0e7ed)),
                  ),
                  margin: EdgeInsets.zero,
                  child: Padding(
                    padding: const EdgeInsets.all(12),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        const Padding(
                          padding: EdgeInsets.all(12),
                          child: Text(
                            '配置工作区',
                            style: TextStyle(
                              fontWeight: FontWeight.bold,
                              fontSize: 18,
                            ),
                          ),
                        ),
                        for (final e in categories.entries)
                          ListTile(
                            selected: category == e.key,
                            selectedTileColor: Theme.of(context)
                                .colorScheme
                                .primaryContainer,
                            leading: Icon(icons[e.key] ?? Icons.settings_outlined),
                            title: Text(e.value),
                            subtitle: Text(
                              descriptions[e.key] ?? e.value,
                              style: const TextStyle(fontSize: 11),
                            ),
                            onTap: busy ? null : () => selectCategory(e.key),
                          ),
                        const Spacer(),
                        const Padding(
                          padding: EdgeInsets.all(12),
                          child: Text(
                            '商城价格与出售天数\n请在商城配置中管理',
                            style: TextStyle(
                              fontSize: 12,
                              color: Colors.blueGrey,
                            ),
                          ),
                        ),
                      ],
                    ),
                  ),
                );
                final body = Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    if (!wide)
                      SingleChildScrollView(
                        scrollDirection: Axis.horizontal,
                        child: Row(
                          children: [
                            for (final e in categories.entries)
                              Padding(
                                padding: const EdgeInsets.only(right: 8),
                                child: ChoiceChip(
                                  label: Text(e.value),
                                  selected: category == e.key,
                                  onSelected: busy
                                      ? null
                                      : (_) => selectCategory(e.key),
                                ),
                              ),
                          ],
                        ),
                      ),
                    sourcePanel(),
                    const SizedBox(height: 14),
                    if (category != 'package') ...[
                      toolbar(),
                      const SizedBox(height: 14),
                    ],
                    Expanded(
                      child: category == 'package'
                          ? ListView(
                              children: [
                                section(
                                  '选择方案与打包',
                                  Column(
                                    crossAxisAlignment:
                                        CrossAxisAlignment.start,
                                    children: [
                                      const Text(
                                        '先预览变化和资源，再生成 OSS 上传包。不会上传或修改线上服务器。',
                                      ),
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
                                                    final r =
                                                        Map<
                                                          String,
                                                          dynamic
                                                        >.from(
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
                                                    final r =
                                                        Map<
                                                          String,
                                                          dynamic
                                                        >.from(
                                                          await call('build', {
                                                            'preview': preview,
                                                          }),
                                                        );
                                                    setState(() {
                                                      report = r;
                                                      message =
                                                          '${r['message']}\n${r['zip']}';
                                                    });
                                                  }),
                                            child: const Text('生成 OSS 更新包'),
                                          ),
                                        ],
                                      ),
                                      if (report != null) ...[
                                        const SizedBox(height: 12),
                                        for (final change
                                            in report!['changes'] as List? ??
                                                [])
                                          Text('• $change'),
                                        ExpansionTile(
                                          title: Text(
                                            '打包文件 ${(report!['files'] as Map?)?.length ?? 0} 项',
                                          ),
                                          children: [
                                            for (final path
                                                in (report!['files'] as Map? ??
                                                        {})
                                                    .keys)
                                              ListTile(
                                                dense: true,
                                                title: SelectableText('$path'),
                                              ),
                                          ],
                                        ),
                                        if (report!['zip'] != null)
                                          SelectableText('${report!['zip']}'),
                                      ],
                                    ],
                                  ),
                                ),
                              ],
                            )
                          : files.isEmpty
                          ? ListView(children: [emptyPanel()])
                          : wide
                          ? Row(
                              crossAxisAlignment: CrossAxisAlignment.stretch,
                              children: [
                                SizedBox(width: 310, child: recordsPanel()),
                                const SizedBox(width: 16),
                                Expanded(child: editorPanel()),
                              ],
                            )
                          : Column(
                              children: [
                                SizedBox(height: 270, child: recordsPanel()),
                                const SizedBox(height: 12),
                                Expanded(child: editorPanel()),
                              ],
                            ),
                    ),
                    if (message.isNotEmpty)
                      Padding(
                        padding: const EdgeInsets.only(top: 10),
                        child: SizedBox(
                          height: 72,
                          child: SingleChildScrollView(
                            child: SelectableText(message),
                          ),
                        ),
                      ),
                    if (folder.isNotEmpty)
                      Padding(
                        padding: const EdgeInsets.only(top: 8),
                        child: Text(
                          '方案目录：$folder',
                          style: const TextStyle(
                            fontSize: 11,
                            color: Colors.blueGrey,
                          ),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                  ],
                );
                return Padding(
                  padding: const EdgeInsets.all(20),
                  child: wide
                      ? Row(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
                          children: [
                            SizedBox(width: 225, child: navigation),
                            const SizedBox(width: 20),
                            Expanded(child: body),
                          ],
                        )
                      : body,
                );
              },
            ),
          ),
        ],
      ),
    ),
  );
}
