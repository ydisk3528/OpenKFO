import 'config_inspect.dart';
import 'client_config.dart';
import 'notice_page.dart';
import 'item_pictures.dart';
import 'batch_grant.dart';
import 'user_management.dart';
import 'banned_words_config.dart';
import 'gm_version.dart';
import 'talisman_config.dart';
import 'reward_config.dart';
import 'stage_config.dart';
import 'stage_unlocks.dart';
import 'task_config.dart';
import 'title_config.dart';
import 'training_config.dart';
import 'weapon_levels_config.dart';
import 'honour_config.dart';
import 'vip_config.dart';
import 'vip_shop_config.dart';
import 'treasure_config.dart';
import 'login_error_config.dart';
import 'shop_config.dart';
import 'wallet_config.dart';

import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';



typedef Api = Future<dynamic> Function(Map<String, dynamic>);

class Backend {
  Backend({this.root, this.localSettings});
  String? localSettings;
  String? root;
  void resolvePaths({
    Directory? executableDirectory,
    Directory? currentDirectory,
  }) {
    final executable =
        executableDirectory ?? File(Platform.resolvedExecutable).parent;
    final settings = File('${executable.path}/gm-settings.json');
    if (root == null && settings.existsSync()) {
      final config =
          jsonDecode(settings.readAsStringSync()) as Map<String, dynamic>;
      String? resolve(dynamic value) {
        if (value is! String || value.isEmpty) return null;
        return Directory(value).isAbsolute
            ? value
            : '${executable.path}/$value';
      }

      root = resolve(config['root']);
      localSettings ??= resolve(config['local_settings']);
    }
    if (root == null) {
      for (final start in [executable, currentDirectory ?? Directory.current]) {
        var d = start;
        for (var n = 0; n < 12; n++) {
          for (final candidate in [
            d.path,
            '${d.path}/kungfukid-local-server',
          ]) {
            if (File('$candidate/runtime-local/online-admin.json')
                .existsSync()) {
              root = candidate;
              break;
            }
          }
          if (root != null) break;
          if (d.parent.path == d.path) break;
          d = d.parent;
        }
        if (root != null) break;
      }
    }
  }

  Future<dynamic> call(Map<String, dynamic> input) async {
    resolvePaths();
    if (root == null) throw Exception('找不到服务器目录，请勿单独移动 EXE。');
    final executable = File(Platform.resolvedExecutable).parent;
    final bundledBackend = File('${executable.path}/kungfu-desktop-admin.exe');
    final p = await Process.start(
      bundledBackend.existsSync()
          ? bundledBackend.path
          : '$root/dist/item-manager/kungfu-desktop-admin.exe',
      [
        '-root',
        root!,
        if (localSettings != null) ...['-local-settings', localSettings!],
      ],
      workingDirectory: root,
    );
    final out = p.stdout.transform(utf8.decoder).join(),
        err = p.stderr.transform(utf8.decoder).join();
    p.stdin.add(utf8.encode(jsonEncode({...input, 'gm_version': gmVersion})));
    await p.stdin.close();
    final code = await p.exitCode, text = await out, error = await err;
    if (code != 0) throw Exception(error);
    final result = jsonDecode(text);
    if (result['ok'] != true) throw Exception(result['error']);
    return result['result'];
  }
}

Future<void> main(List<String> args) async {
  WidgetsFlutterBinding.ensureInitialized();
  final index = args.indexOf('--root');
  final root = index >= 0 && index + 1 < args.length
      ? Directory(args[index + 1]).absolute.path
      : null;
  final localIndex = args.indexOf('--local-settings');
  final localSettings = localIndex >= 0 && localIndex + 1 < args.length
      ? args[localIndex + 1]
      : null;
  final backend = Backend(root: root, localSettings: localSettings);
  runApp(ItemManager(api: backend.call));
}

const teal = Color(0xFF087E83), ink = Color(0xFF172B3A);

class ItemManager extends StatelessWidget {
  const ItemManager({super.key, required this.api, this.startupNotice = ''});
  final String startupNotice;
  final Api api;
  @override
  Widget build(BuildContext context) => MaterialApp(
    debugShowCheckedModeBanner: false,
    title: '功夫小子 · GM管理器',
    theme: ThemeData(
      useMaterial3: true,
      fontFamily: 'Microsoft YaHei',
      colorScheme: ColorScheme.fromSeed(seedColor: teal),
      scaffoldBackgroundColor: const Color(0xFFF3F6F8),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: Colors.white,
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(10),
          borderSide: BorderSide.none,
        ),
      ),
    ),
    home: Manager(api: api, startupNotice: startupNotice),
  );
}

class Manager extends StatefulWidget {
  const Manager({
    super.key,
    required this.api,
    this.onlineOnly = false,
    this.startupNotice = '',
  });
  final String startupNotice;
  final bool onlineOnly;
  final Api api;
  @override
  State<Manager> createState() => _ManagerState();
}

class _ManagerState extends State<Manager> {
  late String environment;
  String get environmentLabel => environment == 'local' ? '本地测试服' : '线上服务器';
  Api get api {
    final target = environment;
    return (request) => widget.api({
      ...request,
      'environment': target,
      'gm_version': gmVersion,
    });
  }

  void switchEnvironment(String value) {
    if (busy || loading || value == environment) return;
    setState(() {
      environment = value;
      itemPictures = ItemPictures(api);
      uid = null;
      accounts = [];
      inventory = [];
      selected.clear();
      pendingGrantId = null;
      pendingGrantSignature = null;
      detail = null;
    });
    load();
  }

  List<Map<String, dynamic>> items = [], accounts = [], inventory = [];
  Map<String, dynamic>? detail;
  final selected = <String>{},
      search = TextEditingController(),
      quantity = TextEditingController(text: '1'),
      durationDays = TextEditingController(text: '365');
  String group = '全部道具',
      gender = '全部性别',
      query = '',
      root = '',
      status = '正在读取完整物品表…';
  int? kind, uid;
  bool versionVerified = false;
  bool loading = true, busy = false, backpack = false, supportedOnly = false;
  String? failure, pendingGrantSignature, pendingGrantId;
  @override
  void initState() {
    super.initState();
    environment = 'online';
    if (widget.startupNotice.isNotEmpty) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted)
          ScaffoldMessenger.of(context)
              .showSnackBar(SnackBar(content: Text(widget.startupNotice)));
      });
    }
    load();
  }

  @override
  void dispose() {
    search.dispose();
    quantity.dispose();
    durationDays.dispose();
    super.dispose();
  }

  List<Map<String, dynamic>> maps(dynamic x) =>
      (x as List).map((e) => Map<String, dynamic>.from(e)).toList();
  Future<void> load() async {
    setState(() {
      loading = true;
      failure = null;
      versionVerified = false;
    });
    try {
      final version = await api({'operation': 'gm_version'});
      if (version['version'] != gmVersion) throw StateError(gmVersionError);
      if (mounted) setState(() => versionVerified = true);
      final data = await api({'operation': 'catalog'}),
          people = maps(await api({'operation': 'accounts'}));
      if (!mounted) return;
      setState(() {
        items = maps(data['items']);
        root = data['root'];
        accounts = people;
        if (!accounts.any((a) => a['uid'] == uid)) {
          uid = accounts.isEmpty ? null : accounts.first['uid'];
        }
        status =
            '$environmentLabel · ${items.length} 件 / ${items.map((i) => i['kind']).toSet().length} 个细分类';
      });
      await refreshInventory();
    } catch (e) {
      if (mounted) setState(() => failure = '$e');
    } finally {
      if (mounted) setState(() => loading = false);
    }
  }

  Future<void> refreshInventory() async {
    final target = uid;
    final data = target == null
        ? <dynamic>[]
        : await api({'operation': 'inventory', 'uid': target});
    if (mounted && uid == target) setState(() => inventory = maps(data));
  }

  String expiryLabel(Map<String, dynamic> item) {
    if (item['duration_state'] == 2) return '已失效';
    final deadline = (item['expires_at'] as num? ?? 0).toInt();
    if (deadline == 0) return '服务器永久有效';
    return '到期：${DateTime.fromMillisecondsSinceEpoch(deadline * 1000).toLocal()}';
  }

  Future<void> editExpiry(Map<String, dynamic> item) async {
    if (busy || uid == null) return;
    final target = uid, call = api, label = environmentLabel;
    var expiryDays = '1';
    setState(() => busy = true);
    try {
      final count = await showDialog<int>(
        context: context,
        builder: (context) {
          String? error;
          return StatefulBuilder(
            builder: (context, update) => AlertDialog(
              title: Text('$label · 物品期限'),
              content: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    'UID $target · 实例 ${item['instance']}\n${expiryLabel(item)}\n只修改此实例的服务器期限；不改变客户端原有期限文字。已失效物品不能恢复。',
                  ),
                  TextFormField(
                    initialValue: expiryDays,
                    onChanged: (value) => expiryDays = value,
                    keyboardType: TextInputType.number,
                    decoration: InputDecoration(
                      labelText: '从现在起有效天数（1–3650）',
                      errorText: error,
                    ),
                  ),
                ],
              ),
              actions: [
                TextButton(
                  onPressed: () => Navigator.pop(context),
                  child: const Text('取消'),
                ),
                TextButton(
                  onPressed: () => Navigator.pop(context, 0),
                  child: const Text('设为永久'),
                ),
                FilledButton(
                  onPressed: () {
                    final value = int.tryParse(expiryDays.trim());
                    if (value == null || value < 1 || value > 3650) {
                      update(() => error = '请输入1–3650的整数');
                      return;
                    }
                    Navigator.pop(context, value);
                  },
                  child: const Text('保存期限'),
                ),
              ],
            ),
          );
        },
      );
      if (count == null || !mounted) return;
      final deadline = count == 0
          ? 0
          : DateTime.now().add(Duration(days: count)).millisecondsSinceEpoch ~/
                1000;
      final result = await call({
        'operation': 'inventory_expiry',
        'uid': target,
        'instance': item['instance'],
        'expires_at': deadline,
        'id':
            'expiry-${DateTime.now().microsecondsSinceEpoch}-$target-${item['instance']}',
      });
      if (!mounted) return;
      setState(() => status = '$label · ${result['message']}');
      await refreshInventory();
    } catch (e) {
      if (mounted) setState(() => status = '期限保存失败：$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Set<String> get owned => inventory.map((i) => i['key'] as String).toSet();
  List<Map<String, dynamic>> get filtered {
    final have = owned;
    return items
        .where(
          (i) =>
              (group == '全部道具' || i['group'] == group) &&
              (kind == null || i['kind'] == kind) &&
              (gender == '全部性别' || i['gender'] == gender) &&
              (!backpack || have.contains(i['key'])) &&
              (!supportedOnly || i['supported'] == true) &&
              ('$i'.toLowerCase().contains(query.toLowerCase())),
        )
        .toList();
  }

  void message(String s) {
    if (!mounted) return;
    setState(() => status = s);
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(s), duration: const Duration(seconds: 6)),
    );
  }

  Future<void> grant() async {
    if (busy || selected.isEmpty || uid == null) return;
    final days = int.tryParse(durationDays.text);
    if (days == null || days < 1 || days > 3650) {
      message('装备期限须为 1–3650 天');
      return;
    }
    final count = int.tryParse(quantity.text);
    if (count == null || count < 1 || count > 999) {
      message('数量须为 1–999 的整数');
      return;
    }
    final keys = selected.toList()..sort();
    final target = uid!, account = accounts.firstWhere((a) => a['uid'] == uid);
    final unready = items
        .where((i) => selected.contains(i['key']) && i['supported'] != true)
        .length;
    final ok = await showDialog<bool>(
      context: context,
      builder: (c) => AlertDialog(
        title: Text('确认添加到$environmentLabel背包'),
        content: Text(
          '角色：${account['nickname']} (${account['account']})\n道具：${keys.length} 种\n武器、服装等装备各 $days 天；消耗道具各增加 $count 个。\n\n${unready > 0 ? '其中 $unready 种仅支持建档，使用效果待适配。\n\n' : ''}写入前保留事务快照。添加后请退出游戏并重新登录。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c, false),
            child: const Text('返回'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(c, true),
            child: const Text('确认添加'),
          ),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    final signature = jsonEncode([target, keys, count, days]);
    if (signature != pendingGrantSignature) {
      pendingGrantSignature = signature;
      pendingGrantId = '${DateTime.now().microsecondsSinceEpoch}-$target';
    }
    setState(() => busy = true);
    try {
      final r = await api({
        'operation': 'grant',
        'uid': target,
        'keys': keys,
        'quantity': count,
        'days': days,
        'id': pendingGrantId,
      });
      await refreshInventory();
      if (!mounted) return;
      setState(() {
        selected.clear();
        pendingGrantId = pendingGrantSignature = null;
      });
      message(
        '添加 ${r['added']} 种，更新 ${r['updated']} 种，已有跳过 ${r['skipped']} 种。重新登录后生效。',
      );
      await showDialog<void>(
        context: context,
        builder: (c) => AlertDialog(
          title: const Text('背包已更新'),
          content: SelectableText(
            '$environmentLabel背包已更新，请退出游戏并重新登录查看。\n\n操作审计：\n${r['backup']}',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(c),
              child: const Text('知道了'),
            ),
          ],
        ),
      );
    } catch (e) {
      message('添加失败：$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> export() async {
    try {
      final folder = Directory('$root/artifacts/item-exports');
      await folder.create(recursive: true);
      final file = File(
        '${folder.path}/道具目录-${DateTime.now().millisecondsSinceEpoch}.json',
      );
      await file.writeAsString(
        const JsonEncoder.withIndent('  ').convert(filtered),
      );
      message('已导出 ${filtered.length} 件：${file.path}');
    } catch (e) {
      message('导出失败：$e');
    }
  }

  late var itemPictures = ItemPictures(api);
  Widget picture(Map<String, dynamic> i, double size) =>
      itemPictures.preview(i, size: size);
  @override
  Widget build(BuildContext context) {
    if (!versionVerified) {
      return Scaffold(
        appBar: AppBar(title: Text('GM管理器 $gmVersion · $environmentLabel')),
        body: Center(
          child: SingleChildScrollView(
            padding: const EdgeInsets.all(24),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                if (loading) const CircularProgressIndicator(),
                SelectableText(failure ?? '正在检查管理版本…'),
                const SizedBox(height: 16),
                TextButton(
                  onPressed: loading ? null : load,
                  child: const Text('重新检查版本'),
                ),
                if (!widget.onlineOnly)
                  TextButton(
                    onPressed: loading
                        ? null
                        : () => switchEnvironment(
                            environment == 'local' ? 'online' : 'local',
                          ),
                    child: Text(environment == 'local' ? '切换线上服务器' : '切换本地测试服'),
                  ),
              ],
            ),
          ),
        ),
      );
    }
    final list = filtered,
        have = owned,
        groups = items.map((i) => i['group'] as String).toSet().toList();
    final kinds = <int, String>{
      for (final i in items.where(
        (i) => group == '全部道具' || i['group'] == group,
      ))
        i['kind']: i['category'],
    };
    return Scaffold(
      body: SafeArea(
        child: Row(
          children: [
            SizedBox(
              width: 196,
              child: Material(
                color: ink,
                child: ListView(
                  key: const ValueKey("gm-sidebar-scroll"),
                  children: [
                    const Padding(
                      padding: EdgeInsets.fromLTRB(22, 28, 16, 5),
                      child: Text(
                        '功夫小子',
                        style: TextStyle(
                          fontSize: 25,
                          fontWeight: FontWeight.w800,
                          color: Colors.white,
                        ),
                      ),
                    ),
                    const Padding(
                      padding: EdgeInsets.fromLTRB(23, 0, 16, 27),
                      child: Text(
                        'GM管理器 $gmVersion',
                        style: TextStyle(color: Color(0xFF9AB1C1)),
                      ),
                    ),
                    Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 12),
                      child: DropdownButtonFormField<String>(
                        initialValue: environment,
                        decoration: const InputDecoration(labelText: '管理环境'),
                        items: [
                          if (!widget.onlineOnly)
                            const DropdownMenuItem(
                              value: 'local',
                              child: Text('本地测试服'),
                            ),
                          DropdownMenuItem(
                            value: 'online',
                            child: Text('线上服务器'),
                          ),
                        ],
                        onChanged: busy || loading
                            ? null
                            : (v) {
                                if (v != null) switchEnvironment(v);
                              },
                      ),
                    ),
                    ExpansionTile(
                      key: const PageStorageKey('玩家管理'),
                      textColor: Colors.white,
                      collapsedTextColor: Colors.white,
                      iconColor: Colors.white,
                      collapsedIconColor: Colors.white,
                      leading: const Icon(
                        Icons.manage_accounts,
                        color: Colors.white,
                      ),
                      title: const Text('玩家管理'),
                      children: [
                        ListTile(
                          textColor: Colors.white, iconColor: Colors.white,
                          leading: const Icon(Icons.campaign), title: const Text('普通通知'),
                          onTap: busy ? null : () => Navigator.push(context, MaterialPageRoute<void>(
                            builder: (_) => NoticePage(api: api, environment: environmentLabel))),
                        ),
                        ListTile(
                          textColor: Colors.white,
                          iconColor: Colors.white,
                          leading: const Icon(Icons.manage_accounts),
                          title: const Text('用户管理'),
                          onTap: busy
                              ? null
                              : () => Navigator.push(
                                  context,
                                  MaterialPageRoute<void>(
                                    builder: (_) => UserManagementPage(
                                      api: api,
                                      environment: environmentLabel,
                                    ),
                                  ),
                                ),
                        ),
                        ListTile(
                          textColor: Colors.white,
                          iconColor: Colors.white,
                          leading: const Icon(Icons.card_membership),
                          title: const Text('VIP管理'),
                          onTap: busy || uid == null
                              ? null
                              : () => Navigator.push(
                                  context,
                                  MaterialPageRoute<void>(
                                    builder: (_) => VipConfigPage(
                                      uid: uid!,
                                      account: accounts
                                          .firstWhere(
                                            (a) => a['uid'] == uid,
                                          )['account']
                                          .toString(),
                                      api: api,
                                      environment: environmentLabel,
                                    ),
                                  ),
                                ),
                        ),
                        ListTile(
                          textColor: Colors.white,
                          leading: const Icon(
                            Icons.lock_open,
                            color: Colors.white,
                          ),
                          title: const Text('个人关卡解锁'),
                          onTap: busy || uid == null
                              ? null
                              : () => Navigator.push(
                                  context,
                                  MaterialPageRoute<void>(
                                    builder: (_) => StageUnlocksPage(
                                      api: api,
                                      environment: environmentLabel,
                                      uid: uid!,
                                    ),
                                  ),
                                ),
                        ),
                        ListTile(
                          textColor: Colors.white,
                          iconColor: Colors.white,
                          leading: const Icon(Icons.card_giftcard),
                          title: const Text('批量发道具 / 点券'),
                          onTap: busy
                              ? null
                              : () => Navigator.push(
                                  context,
                                  MaterialPageRoute<void>(
                                    builder: (_) => BatchGrantPage(
                                      api: api,
                                      environment: environmentLabel,
                                    ),
                                  ),
                                ),
                        ),
                        Material(
                          color: Colors.transparent,
                          child: ListTile(
                            textColor: Colors.white,
                            iconColor: Colors.white,
                            leading: const Icon(Icons.card_giftcard),
                            title: const Text('点券设置与赠送'),
                            onTap: busy
                                ? null
                                : () => Navigator.push(
                                    context,
                                    MaterialPageRoute<void>(
                                      builder: (_) => WalletConfigPage(
                                        api: api,
                                        environment: environmentLabel,
                                      ),
                                    ),
                                  ),
                          ),
                        ),
                      ],
                    ),
                    ExpansionTile(
                      key: const PageStorageKey('奖励与成长'),
                      textColor: Colors.white,
                      collapsedTextColor: Colors.white,
                      iconColor: Colors.white,
                      collapsedIconColor: Colors.white,
                      leading: const Icon(
                        Icons.emoji_events,
                        color: Colors.white,
                      ),
                      title: const Text('奖励与成长'),
                      children: [
                        Material(
                          color: Colors.transparent,
                          child: ListTile(
                            textColor: Colors.white,
                            iconColor: Colors.white,
                            leading: const Icon(Icons.emoji_events),
                            title: const Text('战斗奖励'),
                            onTap: busy
                                ? null
                                : () => Navigator.push(
                                    context,
                                    MaterialPageRoute<void>(
                                      builder: (_) => RewardConfigPage(
                                        api: api,
                                        environmentApi: widget.onlineOnly
                                            ? null
                                            : widget.api,
                                        textCsv: widget.onlineOnly,
                                        environment: environmentLabel,
                                      ),
                                    ),
                                  ),
                          ),
                        ),
                        if (!widget.onlineOnly)
                          ListTile(
                            textColor: Colors.white, iconColor: Colors.white,
                            leading: const Icon(Icons.find_in_page),
                            title: const Text('配置解析'),
                            onTap: busy ? null : () => Navigator.push(context,
                              MaterialPageRoute<void>(builder: (_) => ConfigInspectPage(api: api, environment: environmentLabel))),
                          ),
                        ListTile(
                          textColor: Colors.white,
                          iconColor: Colors.white,
                          leading: const Icon(Icons.school),
                          title: const Text('名侠奖励'),
                          onTap: busy
                              ? null
                              : () => Navigator.push(
                                  context,
                                  MaterialPageRoute<void>(
                                    builder: (_) => TrainingConfigPage(
                                      api: api,
                                      environment: environmentLabel,
                                    ),
                                  ),
                                ),
                        ),
                        ListTile(
                          textColor: Colors.white,
                          iconColor: Colors.white,
                          leading: const Icon(Icons.workspace_premium),
                          title: const Text('称号规则'),
                          onTap: busy
                              ? null
                              : () => Navigator.push(
                                  context,
                                  MaterialPageRoute<void>(
                                    builder: (_) => TitleConfigPage(
                                      api: api,
                                      environment: environmentLabel,
                                      canReadClient: !widget.onlineOnly,
                                    ),
                                  ),
                                ),
                        ),
                        ListTile(
                          textColor: Colors.white,
                          iconColor: Colors.white,
                          leading: const Icon(Icons.military_tech),
                          title: const Text('荣誉规则'),
                          onTap: busy
                              ? null
                              : () => Navigator.push(
                                  context,
                                  MaterialPageRoute<void>(
                                    builder: (_) => HonourConfigPage(
                                      api: api,
                                      environment: environmentLabel,
                                    ),
                                  ),
                                ),
                        ),
                      ],
                    ),
                    ExpansionTile(
                      key: const PageStorageKey('关卡与任务'),
                      textColor: Colors.white,
                      collapsedTextColor: Colors.white,
                      iconColor: Colors.white,
                      collapsedIconColor: Colors.white,
                      leading: const Icon(Icons.map, color: Colors.white),
                      title: const Text('关卡与任务'),
                      children: [
                        ListTile(
                          textColor: Colors.white,
                          iconColor: Colors.white,
                          leading: const Icon(Icons.map),
                          title: const Text('关卡配置'),
                          onTap: busy
                              ? null
                              : () => Navigator.push(
                                  context,
                                  MaterialPageRoute<void>(
                                    builder: (_) => StageConfigPage(
                                      api: api,
                                      environment: environmentLabel,
                                      canReadClient: !widget.onlineOnly,
                                    ),
                                  ),
                                ),
                        ),
                        ListTile(
                          textColor: Colors.white,
                          iconColor: Colors.white,
                          leading: const Icon(Icons.school),
                          title: const Text('任务配置'),
                          onTap: busy
                              ? null
                              : () => Navigator.push(
                                  context,
                                  MaterialPageRoute<void>(
                                    builder: (_) => TaskConfigPage(
                                      api: api,
                                      environment: environmentLabel,
                                      canReadClient: !widget.onlineOnly,
                                    ),
                                  ),
                                ),
                        ),
                      ],
                    ),
                    ExpansionTile(
                      key: const PageStorageKey('装备与商城'),
                      textColor: Colors.white,
                      collapsedTextColor: Colors.white,
                      iconColor: Colors.white,
                      collapsedIconColor: Colors.white,
                      leading: const Icon(
                        Icons.storefront,
                        color: Colors.white,
                      ),
                      title: const Text('装备与商城'),
                      children: [
                        Material(
                          color: Colors.transparent,
                          child: ListTile(
                            textColor: Colors.white,
                            iconColor: Colors.white,
                            leading: const Icon(Icons.storefront),
                            title: const Text('商城配置'),
                            onTap: busy
                                ? null
                                : () => Navigator.push(
                                    context,
                                    MaterialPageRoute<void>(
                                      builder: (_) => ShopConfigPage(
                                        api: api,
                                        environment: environmentLabel,
                                      ),
                                    ),
                                  ),
                          ),
                        ),
                        ListTile(
                          textColor: Colors.white,
                          iconColor: Colors.white,
                          leading: const Icon(Icons.redeem),
                          title: const Text('百宝奖池'),
                          onTap: busy
                              ? null
                              : () => Navigator.push(
                                  context,
                                  MaterialPageRoute<void>(
                                    builder: (_) => TreasureConfigPage(
                                      api: api,
                                      environment: environmentLabel,
                                    ),
                                  ),
                                ),
                        ),
                        ListTile(
                          textColor: Colors.white,
                          iconColor: Colors.white,
                          leading: const Icon(Icons.card_membership),
                          title: const Text('VIP商城折扣'),
                          onTap: busy
                              ? null
                              : () => Navigator.push(
                                  context,
                                  MaterialPageRoute<void>(
                                    builder: (_) => VipShopConfigPage(
                                      api: api,
                                      environment: environmentLabel,
                                    ),
                                  ),
                                ),
                        ),
                        if (!widget.onlineOnly)
                          Material(
                            color: Colors.transparent,
                            child: ListTile(
                              textColor: Colors.white,
                              iconColor: Colors.white,
                              leading: const Icon(Icons.sports_martial_arts),
                              title: const Text('客户端配置'),
                              onTap: busy
                                  ? null
                                  : () => Navigator.push(
                                      context,
                                      MaterialPageRoute<void>(
                                        builder: (_) =>
                                            ClientConfigPage(api: api),
                                      ),
                                    ),
                            ),
                          ),
                        ListTile(
                          textColor: Colors.white,
                          iconColor: Colors.white,
                          leading: const Icon(Icons.school),
                          title: const Text('武器升级'),
                          onTap: busy
                              ? null
                              : () => Navigator.push(
                                  context,
                                  MaterialPageRoute<void>(
                                    builder: (_) => WeaponLevelsConfigPage(
                                      api: api,
                                      environment: environmentLabel,
                                    ),
                                  ),
                                ),
                        ),
                        ListTile(
                          textColor: Colors.white,
                          iconColor: Colors.white,
                          leading: const Icon(Icons.school),
                          title: const Text('宠物／法宝'),
                          onTap: busy
                              ? null
                              : () => Navigator.push(
                                  context,
                                  MaterialPageRoute<void>(
                                    builder: (_) => TalismanConfigPage(
                                      api: api,
                                      environment: environmentLabel,
                                    ),
                                  ),
                                ),
                        ),
                      ],
                    ),
                    ExpansionTile(
                      key: const PageStorageKey('管理设置'),
                      textColor: Colors.white,
                      collapsedTextColor: Colors.white,
                      iconColor: Colors.white,
                      collapsedIconColor: Colors.white,
                      leading: const Icon(Icons.settings, color: Colors.white),
                      title: const Text('管理设置'),
                      children: [
                        ListTile(
                          textColor: Colors.white,
                          iconColor: Colors.white,
                          leading: const Icon(Icons.error_outline),
                          title: const Text('登录错误提示'),
                          onTap: busy ? null : () => Navigator.push(context, MaterialPageRoute<void>(builder: (_) => LoginErrorConfigPage(api: api, environment: environmentLabel))),
                        ),
                        ListTile(
                          textColor: Colors.white,
                          iconColor: Colors.white,
                          leading: const Icon(Icons.block),
                          title: const Text('违禁词管理'),
                          onTap: busy
                              ? null
                              : () => Navigator.push(
                                  context,
                                  MaterialPageRoute<void>(
                                    builder: (_) => BannedWordsPage(
                                      api: api,
                                      environment: environmentLabel,
                                    ),
                                  ),
                                ),
                        ),
                      ],
                    ),
                    ExpansionTile(
                      key: const PageStorageKey('道具分类'),
                      textColor: Colors.white,
                      collapsedTextColor: Colors.white,
                      iconColor: Colors.white,
                      collapsedIconColor: Colors.white,
                      leading: const Icon(
                        Icons.inventory_2,
                        color: Colors.white,
                      ),
                      title: const Text('道具分类'),
                      children: [
                        Column(
                          children: ['全部道具', ...groups]
                              .map(
                                (g) => Padding(
                                  padding: const EdgeInsets.symmetric(
                                    horizontal: 10,
                                    vertical: 3,
                                  ),
                                  child: Material(
                                    color: group == g
                                        ? teal
                                        : Colors.transparent,
                                    borderRadius: BorderRadius.circular(10),
                                    child: ListTile(
                                      dense: true,
                                      textColor: Colors.white,
                                      title: Text(g),
                                      trailing: Text(
                                        '${g == '全部道具' ? items.length : items.where((i) => i['group'] == g).length}',
                                        style: const TextStyle(
                                          fontSize: 12,
                                          color: Color(0xFFB9CDDB),
                                        ),
                                      ),
                                      onTap: () => setState(() {
                                        group = g;
                                        kind = null;
                                      }),
                                    ),
                                  ),
                                ),
                              )
                              .toList(),
                        ),
                      ],
                    ),
                    Padding(
                      padding: const EdgeInsets.all(20),
                      child: Text(
                        widget.onlineOnly
                            ? '线上管理 · HTTPS'
                            : '$environmentLabel · MySQL\n武器方案可应用本机或发布线上',
                        style: TextStyle(
                          color: Color(0xFF9AB1C1),
                          height: 1.8,
                          fontSize: 12,
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            ),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Padding(
                    padding: const EdgeInsets.fromLTRB(24, 22, 24, 16),
                    child: Row(
                      children: [
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(
                                backpack ? '角色背包' : '完整道具库',
                                style: const TextStyle(
                                  fontSize: 26,
                                  fontWeight: FontWeight.w800,
                                  color: ink,
                                ),
                              ),
                              const SizedBox(height: 4),
                              Text(
                                '共 ${items.length} 件 · 已选 ${selected.length} 件',
                                style: const TextStyle(color: Colors.blueGrey),
                              ),
                            ],
                          ),
                        ),
                        SegmentedButton<bool>(
                          segments: const [
                            ButtonSegment(value: false, label: Text('道具库')),
                            ButtonSegment(value: true, label: Text('背包')),
                          ],
                          selected: {backpack},
                          onSelectionChanged: (v) =>
                              setState(() => backpack = v.first),
                        ),
                        IconButton(
                          tooltip: '刷新目录和背包',
                          onPressed: busy ? null : load,
                          icon: const Icon(Icons.refresh),
                        ),
                      ],
                    ),
                  ),
                  Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 24),
                    child: TextField(
                      controller: search,
                      decoration: const InputDecoration(
                        hintText: '搜索名称、编号、说明或原始字段…',
                        prefixIcon: Icon(Icons.search),
                      ),
                      onChanged: (s) => setState(() => query = s),
                    ),
                  ),
                  Padding(
                    padding: const EdgeInsets.fromLTRB(24, 10, 24, 10),
                    child: Wrap(
                      spacing: 12,
                      runSpacing: 6,
                      crossAxisAlignment: WrapCrossAlignment.center,
                      children: [
                        DropdownButton<int>(
                          value: kind ?? -1,
                          underline: const SizedBox(),
                          items: [
                            const DropdownMenuItem(
                              value: -1,
                              child: Text('全部细分类'),
                            ),
                            ...kinds.entries.map(
                              (e) => DropdownMenuItem(
                                value: e.key,
                                child: Text('${e.value} (${e.key})'),
                              ),
                            ),
                          ],
                          onChanged: (v) =>
                              setState(() => kind = v == -1 ? null : v),
                        ),
                        DropdownButton<String>(
                          value: gender,
                          underline: const SizedBox(),
                          items: ['全部性别', '男', '女', '通用']
                              .map(
                                (g) =>
                                    DropdownMenuItem(value: g, child: Text(g)),
                              )
                              .toList(),
                          onChanged: (v) => setState(() => gender = v!),
                        ),
                        FilterChip(
                          label: const Text('已有基础支持'),
                          selected: supportedOnly,
                          onSelected: (v) => setState(() => supportedOnly = v),
                        ),
                        TextButton(
                          onPressed: busy
                              ? null
                              : () => setState(
                                  () => selected.addAll(
                                    list.map((i) => i['key'] as String),
                                  ),
                                ),
                          child: Text('选择当前 ${list.length} 件'),
                        ),
                        TextButton(
                          onPressed: () => setState(() => selected.clear()),
                          child: const Text('清空'),
                        ),
                        IconButton(
                          tooltip: '导出当前结果和完整字段',
                          onPressed: widget.onlineOnly ? null : export,
                          icon: const Icon(Icons.download_outlined),
                        ),
                      ],
                    ),
                  ),
                  Expanded(
                    child: loading
                        ? const Center(child: CircularProgressIndicator())
                        : failure != null
                        ? Center(
                            child: Padding(
                              padding: const EdgeInsets.all(30),
                              child: SelectableText(failure!),
                            ),
                          )
                        : list.isEmpty
                        ? const Center(child: Text('没有匹配的道具'))
                        : GridView.builder(
                            padding: const EdgeInsets.fromLTRB(24, 0, 24, 20),
                            itemCount: list.length,
                            gridDelegate:
                                const SliverGridDelegateWithMaxCrossAxisExtent(
                                  maxCrossAxisExtent: 220,
                                  mainAxisExtent: 210,
                                  crossAxisSpacing: 12,
                                  mainAxisSpacing: 12,
                                ),
                            itemBuilder: (context, index) {
                              final i = list[index],
                                  picked = selected.contains(i['key']);
                              return Material(
                                color: Colors.white,
                                borderRadius: BorderRadius.circular(12),
                                child: InkWell(
                                  borderRadius: BorderRadius.circular(12),
                                  onTap: () => setState(() => detail = i),
                                  child: Container(
                                    decoration: BoxDecoration(
                                      borderRadius: BorderRadius.circular(12),
                                      border: Border.all(
                                        color: picked
                                            ? teal
                                            : const Color(0xFFE1E8ED),
                                        width: picked ? 2 : 1,
                                      ),
                                    ),
                                    padding: const EdgeInsets.all(12),
                                    child: Column(
                                      crossAxisAlignment:
                                          CrossAxisAlignment.start,
                                      children: [
                                        Row(
                                          children: [
                                            picture(i, 58),
                                            const Spacer(),
                                            Checkbox(
                                              value: picked,
                                              onChanged: busy
                                                  ? null
                                                  : (v) => setState(() {
                                                      if (v == true) {
                                                        selected.add(i['key']);
                                                      } else {
                                                        selected.remove(
                                                          i['key'],
                                                        );
                                                      }
                                                      detail = i;
                                                    }),
                                            ),
                                          ],
                                        ),
                                        const SizedBox(height: 10),
                                        Text(
                                          i['name'],
                                          maxLines: 2,
                                          overflow: TextOverflow.ellipsis,
                                          style: const TextStyle(
                                            fontSize: 13,
                                            fontWeight: FontWeight.w700,
                                          ),
                                        ),
                                        const Spacer(),
                                        Text(
                                          '${i['id']} · ${i['category']}',
                                          maxLines: 1,
                                          overflow: TextOverflow.ellipsis,
                                          style: const TextStyle(
                                            fontSize: 11,
                                            color: Colors.blueGrey,
                                          ),
                                        ),
                                        const SizedBox(height: 6),
                                        Row(
                                          children: [
                                            Expanded(
                                              child: Text(
                                                i['supported']
                                                    ? '基础道具'
                                                    : '效果待适配',
                                                style: TextStyle(
                                                  fontSize: 11,
                                                  color: i['supported']
                                                      ? teal
                                                      : Colors.deepOrange,
                                                ),
                                              ),
                                            ),
                                            if (have.contains(i['key']))
                                              const Icon(
                                                Icons.backpack_outlined,
                                                size: 16,
                                                color: teal,
                                              ),
                                          ],
                                        ),
                                      ],
                                    ),
                                  ),
                                ),
                              );
                            },
                          ),
                  ),
                  Container(
                    width: double.infinity,
                    color: Colors.white,
                    padding: const EdgeInsets.symmetric(
                      horizontal: 24,
                      vertical: 10,
                    ),
                    child: Text(
                      status,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        fontSize: 12,
                        color: Colors.blueGrey,
                      ),
                    ),
                  ),
                ],
              ),
            ),
            Container(
              width: 286,
              decoration: const BoxDecoration(
                color: Colors.white,
                border: Border(left: BorderSide(color: Color(0xFFE1E8ED))),
              ),
              padding: const EdgeInsets.all(20),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Text(
                    '发放到角色',
                    style: TextStyle(fontSize: 17, fontWeight: FontWeight.w700),
                  ),
                  const SizedBox(height: 14),
                  DropdownButtonFormField<int>(
                    initialValue: uid,
                    key: ValueKey(uid),
                    isExpanded: true,
                    items: accounts
                        .map(
                          (a) => DropdownMenuItem<int>(
                            value: a['uid'],
                            child: Text(
                              '${a['nickname']} · ${a['account']}',
                              overflow: TextOverflow.ellipsis,
                            ),
                          ),
                        )
                        .toList(),
                    onChanged: busy
                        ? null
                        : (v) async {
                            setState(() => uid = v);
                            try {
                              await refreshInventory();
                            } catch (e) {
                              message('$e');
                            }
                          },
                  ),
                  const SizedBox(height: 8),
                  Text(
                    'UID ${uid ?? "—"} · 背包 ${inventory.length} 条',
                    style: const TextStyle(
                      color: Colors.blueGrey,
                      fontSize: 12,
                    ),
                  ),
                  const Divider(height: 28),
                  Expanded(
                    child: detail == null
                        ? const Center(
                            child: Text(
                              '点击道具卡片查看详情',
                              style: TextStyle(color: Colors.blueGrey),
                            ),
                          )
                        : SingleChildScrollView(
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                picture(detail!, 96),
                                const SizedBox(height: 16),
                                SelectableText(
                                  detail!['name'],
                                  style: const TextStyle(
                                    fontSize: 18,
                                    fontWeight: FontWeight.w700,
                                  ),
                                ),
                                const SizedBox(height: 8),
                                SelectableText(
                                  '编号 ${detail!['id']}\n${detail!['category']} · ${detail!['gender']}',
                                  style: const TextStyle(
                                    color: Colors.blueGrey,
                                    height: 1.7,
                                  ),
                                ),
                                const SizedBox(height: 12),
                                SelectableText(
                                  detail!['description'],
                                  style: const TextStyle(height: 1.7),
                                ),
                                const SizedBox(height: 12),
                                Text(
                                  detail!['supported']
                                      ? '已有基础背包支持，游戏实际效果以实测为准。'
                                      : '可添加到所选环境的背包；该类使用、开箱或活动效果尚未适配。',
                                  style: const TextStyle(
                                    fontSize: 12,
                                    color: Colors.deepOrange,
                                    height: 1.6,
                                  ),
                                ),
                                ...inventory
                                    .where((r) => r['key'] == detail!['key'])
                                    .map(
                                      (r) => Padding(
                                        padding: const EdgeInsets.only(top: 8),
                                        child: Column(
                                          crossAxisAlignment:
                                              CrossAxisAlignment.start,
                                          children: [
                                            Text(
                                              '已拥有 · ${expiryLabel(r)} · 数量 ${r['quantity']} · ${r['slot'] == 0 ? "未穿戴" : "已穿戴"}',
                                              style: const TextStyle(
                                                color: teal,
                                              ),
                                            ),
                                            TextButton(
                                              onPressed:
                                                  busy ||
                                                      r['duration_state'] == 2
                                                  ? null
                                                  : () => editExpiry(r),
                                              child: const Text('修改服务器期限'),
                                            ),
                                          ],
                                        ),
                                      ),
                                    ),
                                Material(
                                  child: ExpansionTile(
                                    tilePadding: EdgeInsets.zero,
                                    title: const Text(
                                      '完整原始配置',
                                      style: TextStyle(fontSize: 13),
                                    ),
                                    children: [
                                      SelectableText(
                                        (detail!['fields'] as List)
                                            .asMap()
                                            .entries
                                            .map(
                                              (e) => '${e.key + 1}  ${e.value}',
                                            )
                                            .join('\n'),
                                        style: const TextStyle(
                                          fontSize: 11,
                                          height: 1.6,
                                        ),
                                      ),
                                    ],
                                  ),
                                ),
                              ],
                            ),
                          ),
                  ),
                  const Divider(height: 24),
                  TextField(
                    controller: durationDays,
                    keyboardType: TextInputType.number,
                    decoration: const InputDecoration(
                      labelText: '装备期限（天）',
                      helperText: '1–3650 天；365 天及以上显示 365+\n按未开始计时发放，扣时及到期待适配',
                      helperMaxLines: 3,
                    ),
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: quantity,
                    keyboardType: TextInputType.number,
                    decoration: const InputDecoration(
                      labelText: '消耗道具数量（个）',
                      helperText: '1–999 个；药水、喇叭、武器切换卡',
                    ),
                  ),
                  const SizedBox(height: 16),
                  SizedBox(
                    width: double.infinity,
                    height: 48,
                    child: FilledButton.icon(
                      onPressed: busy || selected.isEmpty || uid == null
                          ? null
                          : grant,
                      icon: Icon(
                        busy
                            ? Icons.hourglass_top
                            : Icons.add_to_photos_outlined,
                      ),
                      label: Text(busy ? '正在添加…' : '添加已选 ${selected.length} 件'),
                    ),
                  ),
                  const Padding(
                    padding: EdgeInsets.only(top: 10),
                    child: Text(
                      '添加后重新登录游戏生效。\n重复装备跳过，已有穿戴不变。',
                      style: TextStyle(
                        fontSize: 11,
                        color: Colors.blueGrey,
                        height: 1.6,
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
