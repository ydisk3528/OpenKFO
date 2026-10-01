import 'package:flutter/material.dart';

class HornConfigPage extends StatefulWidget {
  const HornConfigPage({
    super.key,
    required this.api,
    required this.environment,
  });
  final Future<dynamic> Function(Map<String, dynamic>) api;
  final String environment;
  @override
  State<HornConfigPage> createState() => _HornConfigPageState();
}

class _HornConfigPageState extends State<HornConfigPage> {
  Map<String, dynamic>? settings;
  bool busy = false;
  String status = '';
  @override
  void initState() {
    super.initState();
    run(false);
  }

  Future<void> run(bool save) async {
    if (busy) return;
    setState(() => busy = true);
    try {
      final result = await widget.api({
        'operation': save ? 'horn_save' : 'horn_get',
        if (save) 'horn': settings,
      });
      final value = Map<String, dynamic>.from(result as Map);
      if (value['revision'] is! int ||
          [
            'channel_enabled',
            'realm_enabled',
            'mood_enabled',
          ].any((k) => value[k] is! bool)) {
        throw const FormatException('服务器未返回完整的喇叭配置，请确认已部署新版服务器');
      }
      if (mounted)
        setState(() {
          settings = value;
          status = save
              ? '已保存；之后受理的喇叭立即使用新配置。已受理的发送可能仍在完成。'
              : '已读取${widget.environment}配置';
        });
    } catch (e) {
      if (mounted) setState(() => status = '操作失败：$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: Text('喇叭管理 · ${widget.environment}')),
    body: Align(
      alignment: Alignment.topCenter,
      child: SizedBox(
        width: 760,
        child: ListView(
          padding: const EdgeInsets.all(24),
          children: [
            const Text(
              '是否允许发送',
              style: TextStyle(fontSize: 22, fontWeight: FontWeight.bold),
            ),
            const SizedBox(height: 12),
            const Text('关闭后拒绝发送且不扣卡。三类喇叭均检查小喇叭卡数量和敏感词。'),
            for (final row in const [
              ['channel_enabled', '全频喇叭', '当前频道的在线玩家（含房间和战斗中），每次 1 张'],
              ['realm_enabled', '全区喇叭', '当前区服的所有在线玩家，每次 3 张'],
              ['mood_enabled', '心情喇叭', '当前区服的所有在线玩家，每次 10 张'],
            ])
              SwitchListTile(
                title: Text(row[1]),
                subtitle: Text(row[2]),
                value: settings?[row[0]] == true,
                onChanged: busy || settings == null
                    ? null
                    : (v) => setState(() => settings![row[0]] = v),
              ),
            const SizedBox(height: 16),
            FilledButton(
              onPressed: busy || settings == null ? null : () => run(true),
              child: const Text('保存配置'),
            ),
            TextButton(
              onPressed: busy ? null : () => run(false),
              child: const Text('重新读取（放弃未保存修改）'),
            ),
            if (busy) const LinearProgressIndicator(),
            const SizedBox(height: 12),
            Text(status),
            const Divider(),
            const Text('敏感词请在「系统管理 → 违禁词管理」维护。大厅、房间聊天、私聊及三类喇叭共用词库。'),
          ],
        ),
      ),
    ),
  );
}
