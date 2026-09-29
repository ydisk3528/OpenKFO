import 'package:flutter/material.dart';

class LoginErrorConfigPage extends StatefulWidget {
  const LoginErrorConfigPage({
    super.key,
    required this.api,
    required this.environment,
  });
  final Future<dynamic> Function(Map<String, dynamic>) api;
  final String environment;
  @override
  State<LoginErrorConfigPage> createState() => _LoginErrorConfigPageState();
}

class _LoginErrorConfigPageState extends State<LoginErrorConfigPage> {
  List<Map<String, dynamic>> catalog = [];
  final fields = <String, TextEditingController>{};
  int? revision;
  bool busy = false;
  String status = '', query = '';
  @override
  void initState() {
    super.initState();
    load();
  }

  @override
  void dispose() {
    for (final c in fields.values) {
      c.dispose();
    }
    super.dispose();
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
    if (!mounted) return;
    final next = List<Map<String, dynamic>>.from(
      (data['catalog'] as List).map((v) => Map<String, dynamic>.from(v as Map)),
    );
    final messages = Map<String, dynamic>.from(data['messages'] as Map);
    setState(() {
      catalog = next;
      revision = data['revision'] as int;
      for (final e in catalog) {
        final code = e['code'] as String;
        (fields[code] ??= TextEditingController()).text =
            messages[code] as String? ?? '';
      }
    });
  }

  Future<void> load() => run(() async {
    apply(await widget.api({'operation': 'login_errors_get'}));
    if (mounted) {
      setState(() => status = '已读取 ${widget.environment} · 版本 $revision');
    }
  });
  Future<void> save() => run(() async {
    final messages = <String, String>{};
    for (final e in catalog) {
      if (e['editable'] == true) {
        final value = fields[e['code']]!.text.trim();
        if (value.runes.length > 300) throw const FormatException('提示最多300字');
        if (value.isNotEmpty) messages[e['code'] as String] = value;
      }
    }
    apply(
      await widget.api({
        'operation': 'login_errors_save',
        'login_errors': {'revision': revision, 'messages': messages},
      }),
    );
    if (mounted) setState(() => status = '已保存。新版服务器和登录组件最长5秒后使用新提示，无需重启。');
  });
  @override
  Widget build(BuildContext context) {
    final editable = !busy && revision != null;
    return Scaffold(
      appBar: AppBar(title: Text('登录错误提示 · ${widget.environment}')),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text('错误码固定，玩家提示可修改；留空使用默认提示。原因 TIPS 仅供管理员排查，不发送给玩家。'),
                const Text('首次需部署新版服务器及登录组件。连接失败和本地组件错误仍使用本地提示；旧组件保留原提示。'),
                TextField(
                  decoration: const InputDecoration(labelText: '搜索错误码或原因'),
                  onChanged: (v) =>
                      setState(() => query = v.trim().toLowerCase()),
                ),
                const SizedBox(height: 8),
                Text(status),
                Wrap(
                  spacing: 12,
                  children: [
                    TextButton(
                      onPressed: busy ? null : load,
                      child: const Text('重新读取'),
                    ),
                    FilledButton(
                      onPressed: editable ? save : null,
                      child: const Text('保存登录提示'),
                    ),
                  ],
                ),
              ],
            ),
          ),
          Expanded(
            child: ListView(
              children: [
                for (final e in catalog.where(
                  (e) => '${e['code']} ${e['default']} ${e['tip']}'
                      .toLowerCase()
                      .contains(query),
                ))
                  Card(
                    margin: const EdgeInsets.fromLTRB(16, 0, 16, 12),
                    child: Padding(
                      padding: const EdgeInsets.all(16),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          SelectableText(
                            e['code'] as String,
                            style: Theme.of(context).textTheme.titleMedium,
                          ),
                          const SizedBox(height: 8),
                          Text('原因 TIPS：${e['tip']}'),
                          const SizedBox(height: 8),
                          Text('默认提示：${e['default']}'),
                          if (e['editable'] == true) ...[
                            TextField(
                              key: ValueKey<String>(e['code'] as String),
                              controller: fields[e['code']],
                              enabled: editable,
                              onChanged: (_) => setState(() {}),
                              maxLength: 300,
                              minLines: 1,
                              maxLines: 4,
                              decoration: const InputDecoration(
                                labelText: '自定义玩家提示（留空使用默认）',
                              ),
                            ),
                            TextButton(
                              onPressed: editable
                                  ? () => setState(
                                      () => fields[e['code']]!.clear(),
                                    )
                                  : null,
                              child: const Text('恢复默认'),
                            ),
                            Text(
                              '玩家将看到：${fields[e['code']]!.text.isEmpty ? e['default'] : fields[e['code']]!.text}\n错误码：${e['code']}',
                            ),
                          ] else
                            const Text('本地提示：只读，不支持服务器热更新'),
                        ],
                      ),
                    ),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
