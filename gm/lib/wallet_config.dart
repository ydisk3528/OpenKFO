import 'package:flutter/material.dart';

class WalletConfigPage extends StatefulWidget {
  const WalletConfigPage({
    super.key,
    required this.api,
    this.environment = '当前环境',
  });
  final String environment;
  final Future<dynamic> Function(Map<String, dynamic>) api;
  @override
  State<WalletConfigPage> createState() => _WalletConfigPageState();
}

class _WalletConfigPageState extends State<WalletConfigPage> {
  List<dynamic> accounts = [];
  int? uid;
  String mode = 'set', message = '';
  bool busy = true;
  final amount = TextEditingController(text: '1000');
  final form = GlobalKey<FormState>();
  String? pendingId;
  String? pendingSignature;

  @override
  void initState() {
    super.initState();
    load();
  }

  @override
  void dispose() {
    amount.dispose();
    super.dispose();
  }

  Future<void> load() async {
    try {
      final result = await widget.api({'operation': 'wallet_accounts'});
      if (!mounted) return;
      setState(() {
        accounts = result;
        uid ??= accounts.isEmpty ? null : accounts.first['uid'];
        busy = false;
      });
    } catch (e) {
      if (mounted) {
        setState(() {
          message = '$e';
          busy = false;
        });
      }
    }
  }

  Future<void> submit() async {
    if (uid == null || !form.currentState!.validate()) return;
    final value = int.parse(amount.text);
    final person = accounts.firstWhere((a) => a['uid'] == uid);
    final accepted = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(mode == 'gift' ? '赠送点券' : '设置点券余额'),
        content: Text(
          '${person['account']}：${person['tickets']} → ${mode == 'gift' ? person['tickets'] + value : value} 点券',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('确认'),
          ),
        ],
      ),
    );
    if (accepted != true || !mounted) return;
    final signature = '$uid/$mode/$value';
    if (signature != pendingSignature) {
      pendingSignature = signature;
      pendingId = 'wallet-${DateTime.now().microsecondsSinceEpoch}';
    }
    setState(() => busy = true);
    try {
      final result = await widget.api({
        'operation': 'wallet_update',
        'uid': uid,
        'mode': mode,
        'amount': value,
        'id': pendingId,
      });
      if (!mounted) return;
      setState(() {
        message = result['message'];
        pendingId = null;
        pendingSignature = null;
      });
      await load();
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          message = '$e';
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: Text('${widget.environment} · 点券余额设置')),
    body: SingleChildScrollView(
      padding: const EdgeInsets.all(24),
      child: SizedBox(
        width: 560,
        child: Form(
          key: form,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              if (busy) const LinearProgressIndicator(),
              Autocomplete<Map<String, dynamic>>(
                initialValue: TextEditingValue(
                  text: uid == null
                      ? ''
                      : '${accounts.firstWhere((a) => a['uid'] == uid)['account']}',
                ),
                displayStringForOption: (a) => '${a['account']}',
                optionsBuilder: (text) => accounts
                    .where(
                      (a) => '${a['account']} ${a['uid']}'
                          .toLowerCase()
                          .contains(text.text.toLowerCase()),
                    )
                    .map((a) => Map<String, dynamic>.from(a)),
                onSelected: (a) => setState(() => uid = a['uid'] as int),
                fieldViewBuilder: (context, controller, focus, submit) =>
                    TextFormField(
                      controller: controller,
                      focusNode: focus,
                      enabled: !busy,
                      decoration: const InputDecoration(
                        labelText: '搜索角色账号 / UID',
                      ),
                      onFieldSubmitted: (_) => submit(),
                      onChanged: (text) {
                        final matches = accounts.where(
                          (a) => '${a['account']}' == text,
                        );
                        setState(
                          () => uid = matches.length == 1
                              ? matches.first['uid']
                              : null,
                        );
                      },
                    ),
              ),
              const SizedBox(height: 24),
              TextFormField(
                controller: amount,
                enabled: !busy,
                keyboardType: TextInputType.number,
                decoration: InputDecoration(
                  labelText: mode == 'gift' ? '赠送数量' : '目标余额',
                ),
                validator: (text) {
                  final n = int.tryParse(text ?? '');
                  return n == null ||
                          n < (mode == 'gift' ? 1 : 0) ||
                          n > 2147483647
                      ? '请输入有效点券数量'
                      : null;
                },
              ),
              const SizedBox(height: 24),
              FilledButton(
                onPressed: busy || uid == null ? null : submit,
                child: const Text('确认操作…'),
              ),
              const SizedBox(height: 16),
              const Text('设置会替换当前总余额并保留审计记录；赠送请到“批量发道具 / 点券”，支持单个玩家与批次进度。'),
              const SizedBox(height: 16),
              SelectableText(message),
            ],
          ),
        ),
      ),
    ),
  );
}
