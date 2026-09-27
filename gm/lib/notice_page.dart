import 'package:flutter/material.dart';

class NoticePage extends StatefulWidget {
  const NoticePage({super.key, required this.api, required this.environment});
  final Future<dynamic> Function(Map<String, dynamic>) api;
  final String environment;
  @override
  State<NoticePage> createState() => _NoticePageState();
}
class _NoticePageState extends State<NoticePage> {
  final text = TextEditingController();
  String? id;
  String message = '', state = '';
  bool busy = false;
  @override
  void dispose() { text.dispose(); super.dispose(); }
  Future<void> submit() async {
    if (text.text.trim().isEmpty) return;
    final confirmed = await showDialog<bool>(context: context, builder: (c) => AlertDialog(
      title: Text('发送到${widget.environment}'),
      content: Text('向当前在线玩家显示以下普通通知：\n\n${text.text.trim()}'),
      actions: [TextButton(onPressed: () => Navigator.pop(c, false), child: const Text('取消')),
        FilledButton(onPressed: () => Navigator.pop(c, true), child: const Text('确认发送'))]));
    if (confirmed != true || !mounted) return;
    id ??= 'notice-${DateTime.now().microsecondsSinceEpoch}';
    await request(true);
  }
  Future<void> request(bool send) async {
    setState(() => busy = true);
    try {
      final r = await widget.api({'operation': send ? 'notice_send' : 'notice_status', 'id': id, 'reason': text.text.trim()});
      if (!mounted) return;
      state = '${r['state']}';
      message = switch (state) {
        'sent' => '已交给 ${r['recipients']} 个在线连接的发送队列。请在游戏内确认显示。',
        'expired' => '通知已过期，未发送。请确认服务器为新版且正在运行。',
        'dispatching' => '正在发送；若服务器意外停止，请核对游戏和日志，不会自动重发。',
        _ => '等待服务器处理，请点击“刷新结果”。超过60秒的通知不再发送。',
      };
    } catch (e) {
      if (mounted) message = '操作结果未确认：$e\n请刷新结果核对；本次编号：$id';
    } finally { if (mounted) setState(() => busy = false); }
  }
  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: Text('普通通知 · ${widget.environment}')),
    body: ListView(padding: const EdgeInsets.all(20), children: [
      const Text('通知以游戏弹窗显示，不踢人、不强制更新。只发送给处理时在线的玩家。'),
      const SizedBox(height: 16),
      TextField(controller: text, enabled: !busy && id == null, maxLength: 99, minLines: 3, maxLines: 5,
        decoration: const InputDecoration(labelText: '通知内容', border: OutlineInputBorder())),
      Wrap(spacing: 12, children: [
        FilledButton(onPressed: busy || id != null ? null : submit, child: const Text('发送普通通知')),
        if (id != null) OutlinedButton(onPressed: busy ? null : () => request(false), child: const Text('刷新结果')),
        if (state == 'sent' || state == 'expired') TextButton(onPressed: busy ? null : () => setState(() { id = null; state = ''; message = ''; text.clear(); }), child: const Text('新通知')),
      ]),
      if (busy) const LinearProgressIndicator(),
      const SizedBox(height: 16), SelectableText(message),
    ]),
  );
}
