import 'item_pictures.dart';

import 'dart:math';

import 'package:flutter/material.dart';

class BatchGrantPage extends StatefulWidget {
  const BatchGrantPage({
    super.key,
    required this.api,
    required this.environment,
  });
  final Future<dynamic> Function(Map<String, dynamic>) api;
  final String environment;
  @override
  State<BatchGrantPage> createState() => _BatchGrantPageState();
}

class _BatchGrantPageState extends State<BatchGrantPage> {
  late final pictures = ItemPictures(widget.api);
  List<dynamic> catalog = [], users = [], history = [];
  final List<Map<String, dynamic>> items = [];
  final Set<int> selected = {};
  Map<String, dynamic>? batch, pendingCreate;
  bool busy = false, running = false, stop = false, all = false;
  String message = '', current = '';
  @override
  void initState() {
    super.initState();
    load();
  }

  @override
  void dispose() {
    stop = true;
    super.dispose();
  }

  Future<void> load() async {
    setState(() => busy = true);
    try {
      final batches = await widget.api({'operation': 'grant_batch_list'});
      if (mounted) setState(() => history = batches);
      final data = await widget.api({'operation': 'catalog'});
      final accounts = await widget.api({'operation': 'accounts'});
      if (mounted) {
        setState(() {
          catalog = data['items'];
          users = accounts;
          history = batches;
        });
      }
    } catch (e) {
      if (mounted) setState(() => message = '$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> selectItems() async {
    String query = '';
    final row = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (c) => StatefulBuilder(
        builder: (c, update) => AlertDialog(
          title: const Text('搜索道具名称或编号'),
          content: SizedBox(
            width: 600,
            height: 440,
            child: Column(
              children: [
                TextField(
                  autofocus: true,
                  onChanged: (v) => update(() => query = v.trim()),
                  decoration: const InputDecoration(labelText: '名称 / ID'),
                ),
                Expanded(
                  child: ListView(
                    children: catalog
                        .where(
                          (i) => '${i['name']} ${i['id']}'
                              .toLowerCase()
                              .contains(query.toLowerCase()),
                        )
                        .take(100)
                        .map(
                          (i) => ListTile(
                            leading: pictures.preview(
                              Map<String, dynamic>.from(i),
                            ),
                            title: Text('${i['name']}'),
                            subtitle: Text('${i['key']} · ${i['category']}'),
                            onTap: () =>
                                Navigator.pop(c, Map<String, dynamic>.from(i)),
                          ),
                        )
                        .toList(),
                  ),
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(c),
              child: const Text('取消'),
            ),
          ],
        ),
      ),
    );
    if (row != null && mounted) {
      setState(() {
        if (items.any((i) => i['key'] == row['key'])) {
          message = '该道具已在清单中，请修改数量';
          return;
        }
        items.add({
          'key': row['key'],
          'name': row['name'],
          'quantity': 1,
          'days': 365,
        });
      });
    }
  }

  Future<void> selectUsers() async {
    String query = '';
    final choice = Set<int>.from(selected);
    final ok = await showDialog<bool>(
      context: context,
      builder: (c) => StatefulBuilder(
        builder: (c, update) => AlertDialog(
          title: Text('指定用户 · 已选 ${choice.length} 人'),
          content: SizedBox(
            width: 600,
            height: 440,
            child: Column(
              children: [
                TextField(
                  onChanged: (v) => update(() => query = v.trim()),
                  decoration: const InputDecoration(
                    labelText: '账号 / 角色名 / UID',
                  ),
                ),
                Expanded(
                  child: ListView(
                    children: users
                        .where(
                          (u) => '${u['account']} ${u['nickname']} ${u['uid']}'
                              .toLowerCase()
                              .contains(query.toLowerCase()),
                        )
                        .map(
                          (u) => CheckboxListTile(
                            value: choice.contains(u['uid']),
                            title: Text('${u['account']} · ${u['nickname']}'),
                            subtitle: Text('UID ${u['uid']}'),
                            onChanged: (v) => update(() {
                              v == true
                                  ? choice.add(u['uid'])
                                  : choice.remove(u['uid']);
                            }),
                          ),
                        )
                        .toList(),
                  ),
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(c, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(c, true),
              child: const Text('确定'),
            ),
          ],
        ),
      ),
    );
    if (ok == true && mounted) {
      setState(() {
        selected
          ..clear()
          ..addAll(choice);
      });
    }
  }

  Future<void> create() async {
    if (pendingCreate == null) {
      if (items.isEmpty ||
          (!all && selected.isEmpty) ||
          items.any(
            (i) =>
                i['quantity'] < 1 ||
                i['quantity'] >
                    (i['key'] == 'currency:ticket' ? 2147483647 : 999) ||
                (i['key'] != 'currency:ticket' &&
                    (i['days'] < 1 || i['days'] > 3650)),
          )) {
        setState(() => message = '请选择发放内容和用户；道具数量1–999，点券1–2147483647');
        return;
      }
      final ok = await showDialog<bool>(
        context: context,
        builder: (c) => AlertDialog(
          title: Text('创建批次 · ${widget.environment}'),
          content: SizedBox(
            width: 560,
            child: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(all ? '全部用户（以服务器创建时名单为准）' : '指定 ${selected.length} 个用户'),
                  for (final i in items)
                    ListTile(
                      leading: i['key'] == 'currency:ticket'
                          ? const Icon(Icons.payments)
                          : pictures.preview(i),
                      title: Text('${i['name']} × ${i['quantity']}'),
                      subtitle: i['key'] == 'currency:ticket'
                          ? null
                          : Text('期限 ${i['days']} 天'),
                    ),
                  const Text('先保存名单和发放内容，点击开始后发放；点券重新登录后刷新。'),
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
              onPressed: () => Navigator.pop(c, true),
              child: const Text('保存批次'),
            ),
          ],
        ),
      );
      if (ok != true || !mounted) return;
      pendingCreate = {
        'operation': 'grant_batch_create',
        'id':
            'gm-${DateTime.now().microsecondsSinceEpoch}-${Random.secure().nextInt(1 << 32)}',
        'all': all,
        'uids': all ? <int>[] : (selected.toList()..sort()),
        'batch_items': items.map((i) => Map<String, dynamic>.from(i)).toList(),
      };
    }
    setState(() => busy = true);
    try {
      final result = await widget.api(pendingCreate!);
      if (mounted) {
        setState(() {
          batch = Map<String, dynamic>.from(result);
          pendingCreate = null;
          message = '批次已保存，可开始发放';
        });
      }
    } catch (e) {
      if (mounted) setState(() => message = '创建结果未确认：$e。请重试原请求，或从历史批次查看。');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> open(String id) async {
    setState(() => busy = true);
    try {
      final result = await widget.api({
        'operation': 'grant_batch_get',
        'id': id,
      });
      if (mounted) setState(() => batch = Map<String, dynamic>.from(result));
    } catch (e) {
      if (mounted) setState(() => message = '$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> run({int? only}) async {
    if (batch == null) return;
    setState(() {
      running = true;
      stop = false;
      message = '发放中';
    });
    final id = batch!['id'];
    try {
      // Refresh durable results before resuming after a timeout or restart.
      final fresh = await widget.api({
        'operation': 'grant_batch_get',
        'id': id,
      });
      if (!mounted) return;
      setState(() => batch = Map<String, dynamic>.from(fresh));
      final targets = (batch!['recipients'] as List)
          .where(
            (r) =>
                r['state'] != 'success' && (only == null || r['uid'] == only),
          )
          .toList();
      for (var offset = 0; offset < targets.length; offset += 20) {
        final group = targets.skip(offset).take(20).toList();
        if (stop || !mounted) break;
        setState(() => current = '正在发放第 ${offset + 1}–${offset + group.length} 人');
        final result = await widget.api({
          'operation': 'grant_batch_send_many',
          'id': id,
          'uids': group.map((row) => row['uid']).toList(),
        });
        if (!mounted) return;
        setState(() => batch = Map<String, dynamic>.from(result));
      }
      if (mounted) {
        setState(() => message = stop ? '已暂停，可继续本批次' : '本轮完成；失败用户可查看原因后重试');
      }
    } catch (e) {
      if (mounted) setState(() => message = '已暂停：$e。结果以服务器记录为准；继续本批次会跳过成功用户。');
    } finally {
      if (mounted) {
        setState(() {
          running = false;
          current = '';
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final locked = busy || running || pendingCreate != null;
    final rows = batch?['recipients'] as List? ?? [];
    final total = (batch?['total'] as num? ?? 0).toInt();
    final success = (batch?['success'] as num? ?? 0).toInt();
    final failed = (batch?['failed'] as num? ?? 0).toInt();
    return PopScope(
      canPop: !running && !busy,
      child: Scaffold(
        appBar: AppBar(title: Text('${widget.environment} · 批量发道具 / 点券')),
        body: Padding(
          padding: const EdgeInsets.all(20),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text('每用户整组发放，失败全部回滚。成功记录保存在服务器，继续同批次不会重复发放。背包请重新登录刷新。'),
              if (busy) const LinearProgressIndicator(),
              Wrap(
                spacing: 12,
                children: [
                  OutlinedButton(
                    onPressed: busy || running
                        ? null
                        : () async {
                            await load();
                            if (!context.mounted) return;
                            final id = await showDialog<String>(
                              context: context,
                              builder: (c) => SimpleDialog(
                                title: const Text('历史批次（最近200批）'),
                                children: history
                                    .map(
                                      (b) => SimpleDialogOption(
                                        onPressed: () =>
                                            Navigator.pop(c, b['id']),
                                        child: Text(
                                          '${b['created']} · ${b['id']}\n成功 ${b['success']}/${b['total']} · 失败 ${b['failed']}',
                                        ),
                                      ),
                                    )
                                    .toList(),
                              ),
                            );
                            if (id != null) await open(id);
                          },
                    child: const Text('历史批次 / 恢复进度'),
                  ),
                  if (batch != null)
                    OutlinedButton(
                      onPressed: locked
                          ? null
                          : () => setState(() {
                              batch = null;
                              message = '';
                            }),
                      child: const Text('新建另一批次'),
                    ),
                ],
              ),
              if (batch == null) ...[
                Wrap(
                  spacing: 12,
                  crossAxisAlignment: WrapCrossAlignment.center,
                  children: [
                    const Text('发放对象'),
                    Switch(
                      value: all,
                      onChanged: locked ? null : (v) => setState(() => all = v),
                    ),
                    Text(all ? '全部用户' : '指定用户'),
                    OutlinedButton(
                      onPressed: locked || all ? null : selectUsers,
                      child: Text('选择用户（${selected.length}）'),
                    ),
                    OutlinedButton(
                      onPressed: locked ? null : selectItems,
                      child: const Text('添加道具'),
                    ),
                    OutlinedButton(
                      onPressed:
                          locked ||
                              items.any((i) => i['key'] == 'currency:ticket')
                          ? null
                          : () => setState(
                              () => items.add({
                                'key': 'currency:ticket',
                                'name': '点券',
                                'quantity': 100,
                                'days': 0,
                              }),
                            ),
                      child: const Text('添加点券'),
                    ),
                  ],
                ),
                Expanded(
                  child: ListView(
                    children: items
                        .map(
                          (i) => Padding(
                            padding: const EdgeInsets.symmetric(vertical: 6),
                            child: Row(
                              children: [
                                i['key'] == 'currency:ticket'
                                    ? const Icon(Icons.payments)
                                    : pictures.preview(i),
                                const SizedBox(width: 8),
                                Expanded(
                                  child: Text('${i['name']}\n${i['key']}'),
                                ),
                                SizedBox(
                                  width: 120,
                                  child: TextFormField(
                                    key: ValueKey('${i['key']}-quantity'),
                                    initialValue: '${i['quantity']}',
                                    enabled: !locked,
                                    keyboardType: TextInputType.number,
                                    decoration: const InputDecoration(
                                      labelText: '每人数量',
                                    ),
                                    onChanged: (v) =>
                                        i['quantity'] = int.tryParse(v) ?? 0,
                                  ),
                                ),
                                const SizedBox(width: 12),
                                SizedBox(
                                  width: 145,
                                  child: i['key'] == 'currency:ticket'
                                      ? const Text('直接增加余额')
                                      : TextFormField(
                                          key: ValueKey('${i['key']}-days'),
                                          initialValue: '${i['days']}',
                                          enabled: !locked,
                                          keyboardType: TextInputType.number,
                                          decoration: const InputDecoration(
                                            labelText: '期限（限时道具）',
                                          ),
                                          onChanged: (v) =>
                                              i['days'] = int.tryParse(v) ?? 0,
                                        ),
                                ),
                                IconButton(
                                  onPressed: locked
                                      ? null
                                      : () => setState(() => items.remove(i)),
                                  icon: const Icon(Icons.delete_outline),
                                ),
                              ],
                            ),
                          ),
                        )
                        .toList(),
                  ),
                ),
                FilledButton(
                  onPressed: busy || running ? null : create,
                  child: Text(pendingCreate == null ? '保存发放批次' : '重试创建原批次'),
                ),
              ] else ...[
                SelectableText('批次 ${batch!['id']}'),
                SizedBox(
                  height: 80,
                  child: ListView(
                    scrollDirection: Axis.horizontal,
                    children: [
                      for (final i in batch!['items'] as List)
                        Padding(
                          padding: const EdgeInsets.only(right: 16),
                          child: Row(
                            children: [
                              i['key'] == 'currency:ticket'
                                  ? const Icon(Icons.payments)
                                  : pictures.preview(
                                      Map<String, dynamic>.from(i),
                                    ),
                              Text('${i['name']} × ${i['quantity']}'),
                            ],
                          ),
                        ),
                    ],
                  ),
                ),
                LinearProgressIndicator(
                  value: total == 0 ? 0 : (success + failed) / total,
                ),
                Text(
                  '已处理 ${success + failed}/$total 人 · 成功 $success · 失败 $failed · 待处理 ${total - success - failed} · 已发 ${batch!['delivered_quantity']} 件 · ${batch!['delivered_tickets'] ?? 0} 点券',
                ),
                if (current.isNotEmpty) Text('正在发送：$current'),
                Wrap(
                  spacing: 12,
                  children: [
                    FilledButton(
                      onPressed: busy || running || success == total
                          ? null
                          : () => run(),
                      child: const Text('开始 / 继续未成功用户'),
                    ),
                    OutlinedButton(
                      onPressed: running
                          ? () => setState(() => stop = true)
                          : null,
                      child: Text(stop ? '正在暂停…' : '暂停（当前用户完成后）'),
                    ),
                    OutlinedButton(
                      onPressed: busy || running
                          ? null
                          : () => open(batch!['id']),
                      child: const Text('刷新记录'),
                    ),
                  ],
                ),
                Expanded(
                  child: ListView.builder(
                    itemCount: rows.length,
                    itemBuilder: (c, n) {
                      final r = rows[n];
                      return ListTile(
                        leading: Icon(
                          r['state'] == 'success'
                              ? Icons.check_circle
                              : r['state'] == 'failed'
                              ? Icons.error
                              : Icons.schedule,
                          color: r['state'] == 'success'
                              ? Colors.green
                              : r['state'] == 'failed'
                              ? Colors.red
                              : null,
                        ),
                        title: Text('${r['account']} · UID ${r['uid']}'),
                        subtitle: SelectableText(
                          '${r['state'] == 'success'
                              ? '成功'
                              : r['state'] == 'failed'
                              ? '失败'
                              : '待发送'} · ${r['updated']}\n${r['detail']}',
                        ),
                        trailing: r['state'] == 'success'
                            ? null
                            : TextButton(
                                onPressed: busy || running
                                    ? null
                                    : () => run(only: r['uid']),
                                child: const Text('单独发送 / 重试'),
                              ),
                      );
                    },
                  ),
                ),
              ],
              if (message.isNotEmpty) SelectableText(message),
            ],
          ),
        ),
      ),
    );
  }
}
