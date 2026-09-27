import 'item_pictures.dart';

import 'dart:math';

import 'package:flutter/material.dart';

class VipConfigPage extends StatefulWidget {
  const VipConfigPage({
    super.key,
    required this.api,
    required this.environment,
    required this.uid,
    required this.account,
  });
  final Future<dynamic> Function(Map<String, dynamic>) api;
  final String environment, account;
  final int uid;
  @override
  State<VipConfigPage> createState() => _VipConfigPageState();
}

class _VipConfigPageState extends State<VipConfigPage> {
  late final pictures = ItemPictures(widget.api);
  final days = TextEditingController(text: '30');
  int kind = 2;
  bool permanent = false, busy = false, loaded = false;
  String status = '';
  Map<String, dynamic>? pending;
  @override
  void initState() {
    super.initState();
    load();
  }

  @override
  void dispose() {
    days.dispose();
    super.dispose();
  }

  String label(int k) =>
      const {1: '非VIP', 2: '白银VIP', 3: '黄金VIP', 4: '铂金VIP'}[k] ?? '未知类型 $k';
  Future<void> load() async {
    if (busy) return;
    setState(() => busy = true);
    try {
      final data = await widget.api({
        'operation': 'vip_get',
        'uid': widget.uid,
      });
      if (!mounted) return;
      final end = data['expires_at'] as int?;
      setState(() {
        loaded = true;
        status =
            '当前：${label(data['kind'] as int)}${data['kind'] == 1
                ? ''
                : end == null
                ? ' · 永久'
                : ' · 到期 ${DateTime.fromMillisecondsSinceEpoch(end * 1000).toLocal()}'}';
      });
    } catch (e) {
      if (mounted) {
        setState(() => status = '读取失败：$e');
      }
    } finally {
      if (mounted) {
        setState(() => busy = false);
      }
    }
  }

  Future<void> grant() async {
    if (busy || !loaded) return;
    if (pending == null) {
      final n = int.tryParse(days.text.trim());
      if (!permanent && (n == null || n < 1 || n > 3650)) {
        setState(() => status = '期限须为1–3650天');
        return;
      }
      final ok = await showDialog<bool>(
        context: context,
        builder: (c) => AlertDialog(
          title: Text('发放到 ${widget.environment}'),
          content: Text(
            '玩家：${widget.account}\nUID：${widget.uid}\n${label(kind)} · ${permanent ? '永久' : '$n 天'}\n\n新增一张会员卡，不延长旧卡。商城折扣由当前环境的「VIP商城折扣」配置决定，发卡本身不开启折扣或发放奖励。发放后请重新登录刷新。',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(c, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(c, true),
              child: const Text('确认发放'),
            ),
          ],
        ),
      );
      if (ok != true || !mounted) return;
      final random = Random.secure();
      final id = List.generate(
        16,
        (_) => random.nextInt(256).toRadixString(16).padLeft(2, '0'),
      ).join();
      pending = {
        'operation': 'vip_grant',
        'id': 'vip-$id',
        'uid': widget.uid,
        'vip_kind': kind,
        'expires_at': permanent
            ? 0
            : DateTime.now().millisecondsSinceEpoch ~/ 1000 + n! * 86400,
      };
    }
    setState(() => busy = true);
    try {
      final result = await widget.api(Map<String, dynamic>.from(pending!));
      if (!mounted) return;
      setState(() {
        pending = null;
        status = '发放成功，实例 ${result['instance']}；请重新登录刷新。';
      });
    } catch (e) {
      if (mounted) {
        setState(() => status = '发放结果未确认：$e\n请重试同一请求，不要另开页面重复发放。');
      }
    } finally {
      if (mounted) {
        setState(() => busy = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final editable = !busy && loaded && pending == null;
    return PopScope(
      canPop: !busy && pending == null,
      child: Scaffold(
        appBar: AppBar(title: Text('VIP管理 · ${widget.environment}')),
        body: ListView(
          padding: const EdgeInsets.all(16),
          children: [
            Text('玩家：${widget.account} · UID ${widget.uid}'),
            const SizedBox(height: 12),
            Text(status),
            const SizedBox(height: 16),
            DropdownButtonFormField<int>(
              initialValue: kind,
              decoration: const InputDecoration(labelText: '发放档位'),
              items: [
                for (final k in [2, 3, 4])
                  DropdownMenuItem(value: k, child: Text(label(k))),
              ],
              onChanged: editable ? (v) => setState(() => kind = v!) : null,
            ),
            if (kind >= 2 && kind <= 4) pictures.byId(730000 + kind - 1),
            SwitchListTile(
              title: const Text('永久资格'),
              value: permanent,
              onChanged: editable ? (v) => setState(() => permanent = v) : null,
            ),
            TextField(
              controller: days,
              enabled: editable && !permanent,
              keyboardType: TextInputType.number,
              decoration: const InputDecoration(labelText: '有效天数（1–3650）'),
            ),
            const SizedBox(height: 16),
            const Text(
              '需要配套新版服务器。不同档位取最高有效档；同档永久优先，否则取最晚期限。商城折扣请在当前环境的「VIP商城折扣」中单独配置；其他会员奖励尚未开放。',
            ),
            if (pending != null)
              SelectableText('待确认请求：${pending!['id']}\n重试不会重复发卡；请保留当前页面完成确认。'),
            const SizedBox(height: 16),
            Wrap(
              spacing: 12,
              children: [
                TextButton(
                  onPressed: busy ? null : load,
                  child: const Text('查询当前资格'),
                ),
                FilledButton(
                  onPressed: !busy && loaded ? grant : null,
                  child: Text(pending == null ? '发放会员卡' : '重试同一请求'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
