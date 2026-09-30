import 'package:flutter/material.dart';

/// 状态/Buff 定制：可视化编辑 ustate.xml 的 <Data> 节点 + 对应
/// ustateeventproc.lua 里的 OnGetUstate_<N> 函数，保存后一键应用到客户端。
class BuffConfigPage extends StatefulWidget {
  const BuffConfigPage({super.key, required this.api});
  final Future<dynamic> Function(Map<String, dynamic>) api;

  @override
  State<BuffConfigPage> createState() => _BuffConfigPageState();
}

class _BuffConfigPageState extends State<BuffConfigPage> {
  List<dynamic> buffs = [];
  List<String> icons = [];
  List<dynamic> apiGroups = [];
  List<dynamic> apiEvents = [];
  String luaEntry = '';
  String? selected;
  String message = '';
  bool busy = false;

  final typeCtrl = TextEditingController();
  final nodeCtrl = TextEditingController();
  final luaCtrl = TextEditingController();

  @override
  void initState() {
    super.initState();
    _bootstrap();
  }

  @override
  void dispose() {
    typeCtrl.dispose();
    nodeCtrl.dispose();
    luaCtrl.dispose();
    super.dispose();
  }

  Future<dynamic> _call(String op, [Map<String, dynamic>? extra]) =>
      widget.api({'operation': op, ...?extra});

  Future<void> _bootstrap() async {
    await _catalog();
    await _icons();
    await _api();
  }

  Future<void> _catalog() async {
    try {
      final r = Map<String, dynamic>.from(await _call('weapon_buff_catalog'));
      if (!mounted) return;
      setState(() {
        buffs = r['buffs'] as List? ?? [];
        luaEntry = '${r['lua_entry'] ?? ''}';
      });
    } catch (e) {
      _fail(e);
    }
  }

  Future<void> _icons() async {
    try {
      final r = Map<String, dynamic>.from(await _call('weapon_buff_icons'));
      if (!mounted) return;
      setState(() => icons = (r['icons'] as List? ?? []).cast<String>());
    } catch (e) {
      // 图标列表拉不到不致命
    }
  }

  Future<void> _api() async {
    try {
      final r = Map<String, dynamic>.from(await _call('weapon_buff_api'));
      if (!mounted) return;
      setState(() {
        apiGroups = r['groups'] as List? ?? [];
        apiEvents = r['events'] as List? ?? [];
      });
    } catch (e) {
      // 参考面板拉不到不致命
    }
  }

  void _fail(Object e) {
    if (mounted) setState(() => message = '$e');
  }

  Future<void> _run(Future<void> Function() work) async {
    if (busy) return;
    setState(() => busy = true);
    try {
      await work();
    } catch (e) {
      _fail(e);
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> _select(String t) async {
    try {
      final d = Map<String, dynamic>.from(
        await _call('weapon_buff_detail', {'key': t}),
      );
      if (!mounted) return;
      setState(() {
        selected = t;
        typeCtrl.text = t;
        final pending = '${d['pending'] ?? ''}';
        nodeCtrl.text = pending.isNotEmpty ? pending : '${d['node'] ?? ''}';
        final luaEdited = '${d['lua_edited'] ?? ''}';
        luaCtrl.text = luaEdited.isNotEmpty ? luaEdited : '${d['lua'] ?? ''}';
        message = '';
      });
    } catch (e) {
      _fail(e);
    }
  }

  void _newNode() {
    setState(() {
      selected = null;
      typeCtrl.clear();
      nodeCtrl.text = _templateNode();
      luaCtrl.text = _templateLua();
      message = '';
    });
  }

  String _templateNode() => '''
<Data type="" ActiveState = "0" TransformStop = "1" DealType = "1" Icon = "Picture\\AbnormalState\\abnormalstate8.png"
\t DeadAction = "0"  AudioEffectId = "" HitDownStop = "0" HitStop = "0" ReliveStop = "1">
\t\t<Behave>
\t\t\t<Effect EffectId = "" BoneId = "1" EffectBindType = "3" />
\t\t</Behave>
\t\t<Logic>
\t\t    <LogicHandle TrigerType = "2" Type = "12" level1 = "0" level2 = "0" level3 = "0" level4 = "0" level5 = "0"/>
\t\t</Logic>
\t\t<Script />
\t</Data>''';

  String _templateLua() => '''
function OnGetUstate_( ustate, self, attackerid )
\t-- 状态生效时被调用（ustate.xml 里要有 TrigerType="2" Type="12"）
\t-- 可用：ustate.id / ustate.level / ustate.duration / ustate.overlap
\treturn;
end''';

  void _applyIcon(String name) {
    final path = 'Picture\\AbnormalState\\$name';
    var t = nodeCtrl.text;
    final re = RegExp(r'Icon\s*=\s*"[^"]*"');
    if (re.hasMatch(t)) {
      t = t.replaceAll(re, 'Icon = "$path"');
    } else {
      t = t.replaceFirst(RegExp(r'<Data\b'), '<Data Icon = "$path"');
    }
    nodeCtrl.text = t;
    setState(() {});
  }

  Future<void> _save() async {
    final t = typeCtrl.text.trim();
    if (t.isEmpty) {
      setState(() => message = '请先填状态号');
      return;
    }
    await _run(() async {
      final r = Map<String, dynamic>.from(
        await _call('weapon_buff_save', {
          'key': t,
          'ustate': {'action': 'upsert', 'text': nodeCtrl.text, 'note': ''},
        }),
      );
      setState(() {
        message = '${r['message']}';
        selected = t;
      });
      await _catalog();
    });
  }

  Future<void> _delete() async {
    final t = typeCtrl.text.trim();
    if (t.isEmpty) return;
    final ok = await showDialog<bool>(
      context: context,
      builder: (c) => AlertDialog(
        title: const Text('删除状态'),
        content: Text('确认删除状态 $t？会在应用后从 ustate.xml 移除。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(c, true),
            child: const Text('删除'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    await _run(() async {
      final r = Map<String, dynamic>.from(
        await _call('weapon_buff_delete', {'key': t}),
      );
      setState(() => message = '${r['message']}');
      await _catalog();
    });
  }

  Future<void> _saveLua() async {
    final t = typeCtrl.text.trim();
    if (t.isEmpty) {
      setState(() => message = '请先填状态号');
      return;
    }
    await _run(() async {
      final r = Map<String, dynamic>.from(
        await _call('weapon_buff_lua_save', {'key': t, 'lua': luaCtrl.text}),
      );
      setState(() => message = '${r['message']}');
    });
  }

  Future<void> _apply() async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (c) => AlertDialog(
        title: const Text('应用到客户端'),
        content: const Text(
          '会把当前所有状态/Buff 编辑连同武器编辑一起写入客户端配置包。\n'
          '请先退出游戏客户端（否则会被拒绝）。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(c, true),
            child: const Text('应用'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    await _run(() async {
      final r = Map<String, dynamic>.from(await _call('weapon_buff_apply'));
      setState(() => message = '${r['message']}');
    });
  }

  Future<void> _exportBuff() async {
    final t = typeCtrl.text.trim();
    if (t.isEmpty) {
      setState(() => message = '请先填状态号');
      return;
    }
    await _run(() async {
      final r = Map<String, dynamic>.from(
        await _call('weapon_buff_export', {
          'key': t,
          'ustate': {'action': 'upsert', 'text': nodeCtrl.text},
          'lua': luaCtrl.text,
        }),
      );
      setState(() => message = '已导出合并包：${r['path']}');
    });
  }

  Future<void> _importBuff() async {
    List<dynamic> pkgs;
    String dir;
    try {
      final r = Map<String, dynamic>.from(await _call('weapon_buff_packages'));
      pkgs = r['packages'] as List? ?? [];
      dir = '${r['directory'] ?? ''}';
    } catch (e) {
      _fail(e);
      return;
    }
    if (pkgs.isEmpty) {
      setState(() => message = '没有可导入的合并包（$dir）');
      return;
    }
    final chosen = await showDialog<String>(
      context: context,
      builder: (c) => SimpleDialog(
        title: const Text('选择要导入的合并包'),
        children: [
          for (final p in pkgs)
            SimpleDialogOption(
              onPressed: () => Navigator.pop(c, '${(p as Map)['path']}'),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text('${p['name']}'),
                  Text(
                    '${p['modified']} · ${p['size']} B',
                    style: const TextStyle(fontSize: 12),
                  ),
                ],
              ),
            ),
        ],
      ),
    );
    if (chosen == null) return;
    final ok = await showDialog<bool>(
      context: context,
      builder: (c) => AlertDialog(
        title: const Text('合并导入'),
        content: Text(
          '会直接写入当前客户端 config.spf2（先自动备份）。\n'
          '请先退出游戏客户端。\n\n$chosen',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(c, true),
            child: const Text('导入'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    await _run(() async {
      final r = Map<String, dynamic>.from(
        await _call('weapon_buff_merge_import', {'source_path': chosen}),
      );
      setState(() => message = '${r['message']}');
      await _catalog();
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('状态/Buff 定制'),
        actions: [
          IconButton(
            onPressed: busy ? null : () => _run(_importBuff),
            icon: const Icon(Icons.file_download_outlined),
            tooltip: '导入合并包',
          ),
          IconButton(
            onPressed: busy ? null : () => _run(_bootstrap),
            icon: const Icon(Icons.refresh),
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
                  width: 320,
                  child: Column(
                    children: [
                      Padding(
                        padding: const EdgeInsets.all(8),
                        child: FilledButton.icon(
                          onPressed: busy ? null : _newNode,
                          icon: const Icon(Icons.add),
                          label: const Text('新建状态'),
                        ),
                      ),
                      Expanded(
                        child: ListView.builder(
                          itemCount: buffs.length,
                          itemBuilder: (c, i) {
                            final b = buffs[i] as Map;
                            final t = '${b['type']}';
                            final edited = '${b['edited'] ?? ''}';
                            return ListTile(
                              dense: true,
                              selected: selected == t,
                              title: Text('状态 $t'),
                              subtitle: Text(
                                [
                                  if (edited.isNotEmpty) '[$edited]',
                                  if (b['lua_function'] == true) 'lua',
                                  if (b['created'] == true) '新增',
                                  if ((b['icon'] ?? '').toString().isNotEmpty)
                                    '${b['icon']}',
                                ].where((e) => e.isNotEmpty).join(' · '),
                                overflow: TextOverflow.ellipsis,
                              ),
                              onTap: busy ? null : () => _run(() => _select(t)),
                            );
                          },
                        ),
                      ),
                    ],
                  ),
                ),
                const VerticalDivider(width: 1),
                Expanded(child: _editor()),
              ],
            ),
          ),
          if (message.isNotEmpty)
            Container(
              width: double.infinity,
              color: Theme.of(context).colorScheme.surfaceContainerHighest,
              padding: const EdgeInsets.all(12),
              child: Text(message),
            ),
        ],
      ),
    );
  }

  Widget _editor() {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Row(
          children: [
            SizedBox(
              width: 160,
              child: TextField(
                controller: typeCtrl,
                decoration: const InputDecoration(
                  labelText: '状态号',
                  hintText: '如 432 / 433',
                ),
              ),
            ),
            const SizedBox(width: 12),
            Text('lua 函数：OnGetUstate_${typeCtrl.text.trim()}'),
          ],
        ),
        const SizedBox(height: 8),
        const Text('图标（点击替换节点里的 Icon）'),
        Wrap(
          spacing: 4,
          runSpacing: 4,
          children: [
            for (final name in icons)
              ActionChip(
                label: Text(name),
                onPressed: busy ? null : () => _applyIcon(name),
              ),
          ],
        ),
        const SizedBox(height: 12),
        Row(
          children: [
            const Text('节点 XML（ustate.xml 的 <Data>）'),
            const Spacer(),
            TextButton.icon(
              onPressed: busy ? null : _newNode,
              icon: const Icon(Icons.auto_fix_high),
              label: const Text('模板'),
            ),
          ],
        ),
        TextField(
          controller: nodeCtrl,
          maxLines: 12,
          minLines: 8,
          keyboardType: TextInputType.multiline,
          decoration: const InputDecoration(
            border: OutlineInputBorder(),
            hintText: '<Data type="..." ...>...</Data>',
            alignLabelWithHint: true,
          ),
          style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
        ),
        const SizedBox(height: 12),
        Text('lua 函数（保存到 $luaEntry）'),
        TextField(
          controller: luaCtrl,
          maxLines: 16,
          minLines: 10,
          keyboardType: TextInputType.multiline,
          decoration: const InputDecoration(
            border: OutlineInputBorder(),
            alignLabelWithHint: true,
          ),
          style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
        ),
        const SizedBox(height: 12),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            FilledButton(
              onPressed: busy ? null : () => _run(_save),
              child: const Text('保存状态'),
            ),
            FilledButton(
              onPressed: busy ? null : () => _run(_saveLua),
              child: const Text('保存 lua'),
            ),
            OutlinedButton(
              onPressed: busy ? null : _delete,
              child: const Text('删除状态'),
            ),
            OutlinedButton.icon(
              onPressed: busy ? null : _exportBuff,
              icon: const Icon(Icons.ios_share),
              label: const Text('导出合并包'),
            ),
            FilledButton.tonal(
              onPressed: busy ? null : _apply,
              child: const Text('应用到客户端'),
            ),
          ],
        ),
        const SizedBox(height: 16),
        _apiPanel(),
      ],
    );
  }

  Widget _apiPanel() {
    return ExpansionTile(
      title: const Text('Player.* 方法参考（从客户端现有 lua 反查）'),
      children: [
        for (final g in apiGroups)
          ExpansionTile(
            title: Text('${(g as Map)['title']}'),
            children: [
              for (final it in ((g['items'] as List? ?? [])))
                _apiItem(it as Map),
            ],
          ),
        ExpansionTile(
          title: const Text('引擎回调函数名'),
          children: [
            for (final e in apiEvents) _apiItem(e as Map),
          ],
        ),
      ],
    );
  }

  Widget _apiItem(Map m) {
    return ListTile(
      dense: true,
      title: Text('${m['name']}', style: const TextStyle(fontFamily: 'monospace')),
      subtitle: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SelectableText(
            '${m['signature'] ?? ''}',
            style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
          ),
          if ((m['desc'] ?? '').toString().isNotEmpty)
            Text('${m['desc']}', style: const TextStyle(fontSize: 12)),
        ],
      ),
    );
  }
}
