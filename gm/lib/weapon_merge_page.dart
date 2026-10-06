import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

/// ZIP imports have their own working copy; viewing a diff never writes a game.
class WeaponMergePage extends StatefulWidget {
  const WeaponMergePage({super.key, required this.api, this.clientConfig});
  final Map<String, dynamic>? clientConfig;
  final Future<dynamic> Function(Map<String, dynamic>) api;
  @override
  State<WeaponMergePage> createState() => _WeaponMergePageState();
}

class _WeaponMergePageState extends State<WeaponMergePage> {
  final source = TextEditingController();
  List<dynamic> packages = [];
  Map<String, dynamic>? result;
  String workspace = '',
      workspaceProfile = '',
      workspaceHash = '',
      message = '';
  bool busy = false, needsSave = false, applied = false;
  int clientGeneration = 0;
  @override
  void initState() {
    super.initState();
    loadPackages();
  }

  @override
  void didUpdateWidget(covariant WeaponMergePage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!mapEquals(oldWidget.clientConfig, widget.clientConfig)) {
      clientGeneration++;
      workspace = workspaceProfile = workspaceHash = '';
      result = null;
      needsSave = applied = false;
      message = '';
    }
  }

  bool sameWorkspace(
    String id,
    String profile,
    String hash,
    Map<String, dynamic>? clientConfig,
    int generation,
  ) =>
      mounted &&
      clientGeneration == generation &&
      workspace == id &&
      workspaceProfile == profile &&
      workspaceHash == hash &&
      mapEquals(widget.clientConfig, clientConfig);

  Map<String, dynamic>? captureClientConfig() => widget.clientConfig == null
      ? null
      : Map<String, dynamic>.from(widget.clientConfig!);

  @override
  void dispose() {
    source.dispose();
    super.dispose();
  }

  Future<void> loadPackages() async {
    try {
      final r = await widget.api({'operation': 'weapon_merge_packages'});
      if (mounted) setState(() => packages = r['packages'] as List? ?? []);
    } catch (e) {
      if (mounted) setState(() => message = '读取最近文件失败：$e');
    }
  }

  Future<bool> task(Future<void> Function() work) async {
    if (busy) return false;
    setState(() => busy = true);
    try {
      await work();
      return true;
    } catch (e) {
      if (mounted) setState(() => message = '$e');
      return false;
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> chooseFile() async {
    // 原生文件对话框（file_selector）在部分部署下抛 MissingPluginException，
    // 改用自绘「最近武器包」列表；也可以直接在文本框填写 ZIP 路径。
    if (packages.isEmpty) {
      setState(() => message = '没有最近的武器包，请直接在文本框填写 ZIP 路径');
      return;
    }
    final picked = await showDialog<String>(
      context: context,
      builder: (c) => SimpleDialog(
        title: const Text('选择武器合并包'),
        children: [
          for (final p in packages)
            SimpleDialogOption(
              onPressed: () => Navigator.pop(c, '${(p as Map)['path']}'),
              child: Text('${p['name']}'),
            ),
        ],
      ),
    );
    if (picked == null || !mounted) return;
    source.text = picked;
    await compare();
  }

  Future<void> compare() async {
    final path = source.text.trim();
    if (path.isEmpty) {
      setState(() => message = '请先选择武器 ZIP');
      return;
    }
    Map<String, dynamic>? preview;
    final compareWorkspace = workspace;
    final compareProfile = workspaceProfile;
    final compareHash = workspaceHash;
    final clientConfig = captureClientConfig();
    final generation = clientGeneration;
    bool current() => sameWorkspace(
      compareWorkspace,
      compareProfile,
      compareHash,
      clientConfig,
      generation,
    );
    final ok = await task(() async {
      preview = Map<String, dynamic>.from(
        await widget.api({
          'operation': 'weapon_merge_compare',
          'source_path': path,
          'merge_workspace': compareWorkspace,
          'client_config': ?clientConfig,
        }),
      );
    });
    if (!ok || !mounted || !current()) return;
    final comparedProfile = '${preview!['active_profile'] ?? ''}';
    final comparedHash = '${preview!['source_hash'] ?? ''}';
    final ids = await showDialog<List<int>>(
      context: context,
      builder: (_) => _MergeSelection(preview: preview!),
    );
    if (ids == null || ids.isEmpty || !mounted) return;
    if (!current()) return;
    await task(() async {
      final r = Map<String, dynamic>.from(
        await widget.api({
          'operation': 'weapon_merge_stage',
          'source_path': path,
          'merge_workspace': compareWorkspace,
          'client_config': ?clientConfig,
          'active_profile': comparedProfile,
          'source_hash': comparedHash,
          'merge_weapons': ids,
          'revision': preview!['revision'],
        }),
      );
      if (!current()) return;
      setState(() {
        result = r;
        workspace = '${r['workspace'] ?? ''}';
        workspaceProfile = '${r['active_profile'] ?? comparedProfile}';
        workspaceHash = '${r['source_hash'] ?? comparedHash}';
        needsSave = true;
        message = r['message'];
      });
    });
  }

  Future<bool> save() async {
    // 原生保存对话框（file_selector）在部分部署下抛 MissingPluginException，
    // 改为不传路径，由后端存到固定快照目录；消息里会显示完整路径。
    final saveWorkspace = workspace;
    final saveProfile = workspaceProfile;
    final saveHash = workspaceHash;
    final clientConfig = captureClientConfig();
    final generation = clientGeneration;
    return await task(() async {
      final r = await widget.api({
        'operation': 'weapon_merge_save',
        'merge_workspace': saveWorkspace,
        'active_profile': saveProfile,
        'source_hash': saveHash,
        'client_config': ?clientConfig,
      });
      if (sameWorkspace(
        saveWorkspace,
        saveProfile,
        saveHash,
        clientConfig,
        generation,
      )) {
        setState(() {
          needsSave = false;
          message = '${r['message']}\n${r['path']}';
        });
      }
    });
  }

  Future<void> apply() async {
    final applyWorkspace = workspace;
    final applyProfile = workspaceProfile;
    final applyHash = workspaceHash;
    final clientConfig = captureClientConfig();
    final generation = clientGeneration;
    bool current() => sameWorkspace(
      applyWorkspace,
      applyProfile,
      applyHash,
      clientConfig,
      generation,
    );
    final confirm = await showDialog<bool>(
      context: context,
      builder: (c) => AlertDialog(
        title: const Text('应用临时配置到游戏？'),
        content: const Text(
          '将备份并替换当前客户端的配置和此次导入的资源。请先关闭游戏；游戏文件若已被其他操作修改，会拒绝覆盖。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(c, true),
            child: const Text('确认应用'),
          ),
        ],
      ),
    );
    if (confirm != true || !current()) return;
    await task(() async {
      final r = await widget.api({
        'operation': 'weapon_merge_apply',
        'merge_workspace': applyWorkspace,
        'active_profile': applyProfile,
        'source_hash': applyHash,
        'client_config': ?clientConfig,
      });
      if (current()) {
        setState(() {
          applied = true;
          needsSave = false;
          workspace = '';
          workspaceProfile = '${r['active_profile'] ?? workspaceProfile}';
          workspaceHash = '${r['source_hash'] ?? workspaceHash}';
          message = '${r['message']}\n备份：${r['backup']}';
        });
      }
    });
  }

  Future<void> leave() async {
    if (busy) return;
    if (needsSave) {
      final choice = await showDialog<String>(
        context: context,
        builder: (c) => AlertDialog(
          title: const Text('临时配置尚未保存'),
          content: const Text('可以保存配置及新增资源的 ZIP 快照。返回不会修改游戏文件。'),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(c),
              child: const Text('继续编辑'),
            ),
            TextButton(
              onPressed: () => Navigator.pop(c, 'discard'),
              child: const Text('不保存返回'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(c, 'save'),
              child: const Text('保存后返回'),
            ),
          ],
        ),
      );
      if (choice == null || !mounted) return;
      if (choice == 'save' && !await save()) return;
    }
    if (!mounted) return;
    setState(() => needsSave = false);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) Navigator.pop(context, applied);
    });
  }

  Widget panel(String title, String description, Widget child) => Card(
    color: Colors.white,
    elevation: 0,
    shape: RoundedRectangleBorder(
      borderRadius: BorderRadius.circular(12),
      side: const BorderSide(color: Color(0xffe0e7ed)),
    ),
    child: Padding(
      padding: const EdgeInsets.all(20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(title, style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 6),
          Text(description, style: Theme.of(context).textTheme.bodySmall),
          const SizedBox(height: 18),
          child,
        ],
      ),
    ),
  );
  @override
  Widget build(BuildContext context) => PopScope(
    canPop: !busy && !needsSave,
    onPopInvokedWithResult: (didPop, _) {
      if (!didPop) leave();
    },
    child: Scaffold(
      appBar: AppBar(
        title: const Text('武器包合并'),
        leading: IconButton(
          onPressed: busy ? null : leave,
          icon: const Icon(Icons.arrow_back),
        ),
      ),
      body: Column(
        children: [
          if (busy) const LinearProgressIndicator(),
          Expanded(
            child: Center(
              child: ConstrainedBox(
                constraints: const BoxConstraints(maxWidth: 1180),
                child: ListView(
                  padding: const EdgeInsets.all(24),
                  children: [
                    const Text(
                      '选择 ZIP  →  对比并选择武器  →  合并到临时配置  →  保存或应用',
                      style: TextStyle(fontWeight: FontWeight.w600),
                    ),
                    const SizedBox(height: 16),
                    panel(
                      '导入来源',
                      '支持连续合并多个武器包。每次都以当前临时配置为参照；原游戏配置保持不变。',
                      Column(
                        children: [
                          TextField(
                            controller: source,
                            enabled: !busy,
                            decoration: InputDecoration(
                              labelText: '武器 ZIP 文件',
                              border: const OutlineInputBorder(),
                              suffixIcon: IconButton(
                                tooltip: '选择文件',
                                onPressed: busy ? null : chooseFile,
                                icon: const Icon(Icons.folder_open),
                              ),
                            ),
                          ),
                          const SizedBox(height: 12),
                          Row(
                            children: [
                              FilledButton.icon(
                                onPressed: busy ? null : chooseFile,
                                icon: const Icon(Icons.file_open),
                                label: const Text('选择武器 ZIP'),
                              ),
                              const SizedBox(width: 12),
                              OutlinedButton.icon(
                                onPressed: busy ? null : compare,
                                icon: const Icon(Icons.compare_arrows),
                                label: const Text('对比差异'),
                              ),
                            ],
                          ),
                          if (packages.isNotEmpty)
                            ExpansionTile(
                              title: Text('最近的武器包 · ${packages.length} 个'),
                              children: [
                                SizedBox(
                                  height: 180,
                                  child: ListView.builder(
                                    itemCount: packages.length,
                                    itemBuilder: (_, i) => ListTile(
                                      title: Text('${packages[i]['name']}'),
                                      subtitle: Text('${packages[i]['path']}'),
                                      leading: const Icon(
                                        Icons.folder_zip_outlined,
                                      ),
                                      onTap: busy
                                          ? null
                                          : () {
                                              source.text = packages[i]['path'];
                                              compare();
                                            },
                                    ),
                                  ),
                                ),
                              ],
                            ),
                        ],
                      ),
                    ),
                    panel(
                      '临时配置',
                      workspace.isEmpty ? '尚未合并武器包，或已完成应用。' : '以下内容仅位于临时工作目录。',
                      Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          if (result != null) ...[
                            SelectableText('${result!['path']}'),
                            const SizedBox(height: 8),
                            Text(
                              '本次新增 ${(result!['new'] as List? ?? []).length} 把 · 更新 ${(result!['modified'] as List? ?? []).length} 把 · 导入 ${result!['assets'] ?? 0} 个资源',
                            ),
                            ExpansionTile(
                              title: const Text('查看本次配置条目'),
                              children: [
                                SizedBox(
                                  height: 200,
                                  child: ListView.builder(
                                    itemCount:
                                        (result!['entries'] as List? ?? [])
                                            .length,
                                    itemBuilder: (_, i) {
                                      final row = result!['entries'][i];
                                      return ListTile(
                                        dense: true,
                                        title: Text('${row['entry']}'),
                                        subtitle: Text('${row['detail']}'),
                                      );
                                    },
                                  ),
                                ),
                              ],
                            ),
                          ],
                          Wrap(
                            spacing: 12,
                            runSpacing: 8,
                            children: [
                              OutlinedButton.icon(
                                onPressed: busy || workspace.isEmpty
                                    ? null
                                    : save,
                                icon: const Icon(Icons.save_alt),
                                label: const Text('保存配置与资源 ZIP'),
                              ),
                              FilledButton.icon(
                                onPressed: busy || workspace.isEmpty
                                    ? null
                                    : apply,
                                icon: const Icon(Icons.check),
                                label: const Text('应用到游戏'),
                              ),
                            ],
                          ),
                          const SizedBox(height: 10),
                          const Text(
                            '保存的是完整配置和此次导入资源的快照，不是用于再次导入的武器合并包。',
                            style: TextStyle(fontSize: 12),
                          ),
                        ],
                      ),
                    ),
                    if (message.isNotEmpty)
                      Card(
                        child: Padding(
                          padding: const EdgeInsets.all(16),
                          child: SelectableText(message),
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
  );
}

class _MergeSelection extends StatefulWidget {
  const _MergeSelection({required this.preview});
  final Map<String, dynamic> preview;
  @override
  State<_MergeSelection> createState() => _MergeSelectionState();
}

class _MergeSelectionState extends State<_MergeSelection> {
  late final rows = <Map<String, dynamic>>[
    for (final r in widget.preview['new'] as List? ?? [])
      {...Map<String, dynamic>.from(r), 'status': '新增'},
    for (final r in widget.preview['modified'] as List? ?? [])
      {...Map<String, dynamic>.from(r), 'status': '更新'},
    for (final r in widget.preview['unchanged'] as List? ?? [])
      {...Map<String, dynamic>.from(r), 'status': '相同'},
  ];
  late final selected = {
    for (final r in rows)
      if (r['status'] != '相同') r['id'] as int,
  };
  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('初始化完成 · 选择差异合并'),
    content: SizedBox(
      width: 720,
      height: 420,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text('只合并勾选的武器。下一步写入临时配置，不会替换游戏文件。'),
          const SizedBox(height: 8),
          Text(
            '参照：${widget.preview['reference']}',
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
          ),
          const Divider(),
          Expanded(
            child: ListView.builder(
              itemCount: rows.length,
              itemBuilder: (_, i) {
                final r = rows[i];
                return CheckboxListTile(
                  value: selected.contains(r['id']),
                  title: Text('${r['name']} · ${r['id']}'),
                  secondary: Chip(label: Text('${r['status']}')),
                  subtitle: Text(
                    (r['changes'] as List? ?? []).join('、').isEmpty
                        ? (r['status'] == '相同'
                              ? '武器配置相同；可勾选以补入包内资源。'
                              : '新增武器定义及动作依赖')
                        : (r['changes'] as List).join('、'),
                  ),
                  onChanged: (v) => setState(() {
                    v == true
                        ? selected.add(r['id'])
                        : selected.remove(r['id']);
                  }),
                );
              },
            ),
          ),
        ],
      ),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('取消'),
      ),
      FilledButton(
        onPressed: selected.isEmpty
            ? null
            : () => Navigator.pop(context, selected.toList()),
        child: Text('合并 ${selected.length} 把到临时配置'),
      ),
    ],
  );
}
