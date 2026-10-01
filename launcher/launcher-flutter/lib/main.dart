import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:path/path.dart' as p;

import 'launcher_service.dart';
import 'frame_mode.dart';
import 'update_service.dart';
import 'update_progress_view.dart';
import 'log_export.dart';
import 'connection_error.dart';
import 'classic_skin.dart';
import 'package:file_selector/file_selector.dart';

const launcherDisplayVersion = 'V1.2';
const launcherVersion = String.fromEnvironment('LAUNCHER_VERSION', defaultValue: 'development');

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  runApp(const LauncherApp());
}

class LauncherApp extends StatelessWidget {
  const LauncherApp({super.key});
  @override
  Widget build(BuildContext context) => MaterialApp(
    debugShowCheckedModeBanner: false,
    title: '启动器v1.1',
    theme: ThemeData(
      useMaterial3: true,
      colorScheme: ColorScheme.fromSeed(seedColor: const Color(0xffd44817), brightness: Brightness.light),
      fontFamily: 'Microsoft YaHei',
      scaffoldBackgroundColor: const Color(0xfffff4e5),
      inputDecorationTheme: const InputDecorationTheme(
        filled: true, fillColor: Colors.white,
        labelStyle: TextStyle(color: Color(0xff5f3823)),
      ),
    ),
    home: const LauncherPage(),
  );
}

class LauncherPage extends StatefulWidget {
  const LauncherPage({super.key, this.preview = false});
  final bool preview;
  @override
  State<LauncherPage> createState() => _LauncherPageState();
}

class _LauncherPageState extends State<LauncherPage> {
  final service = LauncherService(p.dirname(Platform.resolvedExecutable));
  late final updates = UpdateService(service, onProgress: (value) { if(mounted) setState(()=>transfer=value); });
  UpdateProgress? transfer;
  final account = TextEditingController(), password = TextEditingController();
  final accounts = List.generate(
    8,
    (_) => <String, dynamic>{'Account': '', 'Password': ''},
  );
  int selected = 1;
  final running = <int, bool>{};
  Timer? stateTimer;
  bool readingState = false;

  Future<void> refreshRunning() async {
    if (!ready || readingState) return;
    readingState = true;
    final n = selected;
    try {
      final alive = await service.state(n) != null;
      if (mounted) setState(() => running[n] = alive);
    } catch (_) {
      if (mounted) setState(() => running.remove(n));
    } finally {
      readingState = false;
    }
  }
  bool busy = true, ready = false, gameReady = false, hide = false, fps = true;
  FrameMode frameMode = FrameMode.normal;
  String status = '正在准备启动器…', health = '正在检查服务器…';
  bool updateBlocked = true;
  Map<String, dynamic>? release;
  String announcement = '暂无公告';
  bool readingAnnouncement = false;

  Future<void> refreshAnnouncement() async {
    if (!ready || readingAnnouncement) return;
    setState(() => readingAnnouncement = true);
    try {
      final text = await updates.announcement();
      if (mounted) setState(() => announcement = text);
    } catch (_) {
      if (mounted) setState(() => announcement = '公告暂时无法加载，请稍后刷新。');
    } finally {
      if (mounted) setState(() => readingAnnouncement = false);
    }
  }
  File get preferences => File(
    p.join(
      Platform.environment['LOCALAPPDATA']!,
      'OpenKFO',
      'Launcher',
      'flutter-preferences.json',
    ),
  );
  @override
  void initState() {
    super.initState();
    if(widget.preview) { busy=false; ready=false; status="本地布局预览"; } else { WidgetsBinding.instance.addPostFrameCallback((_) => initialize()); }
  }

  void report(String text) {
    if (mounted) setState(() => status = publicError(text));
  }

  Future<void> initialize() async {
    try {
      await service.init();
      gameReady = await LauncherService.isGameDirectory(service.game);
      if (await preferences.exists()) {
        final m = jsonDecode(await preferences.readAsString());
        frameMode = FrameMode.values.where((v) => v.name == m['frame_mode']).firstOrNull
            ?? (m['high'] == true ? FrameMode.high125 : FrameMode.normal);
        fps = m['fps'] != false;
        hide = m['hide'] == true;
      }
      for (var n = 1; n <= 8; n++) {
        accounts[n - 1] = await service.loadAccount(n);
      }
      loadFields();
      ready = true;
      unawaited(refreshAnnouncement());
      await refreshRunning();
      stateTimer = Timer.periodic(const Duration(seconds: 2), (_) {
        if (!busy) unawaited(refreshRunning());
      });
      report('选择窗口，填写账号后启动游戏');
      await refreshHealth();
      await checkUpdate();
    } catch (e) {
      await error(e);
    } finally {
      await refreshRunning();
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> selectRealm(String id) async {
    if (busy || id == service.realmId) return;
    setState(() => busy = true);
    try {
      await save();
      await service.selectRealm(id);
      account.clear(); password.clear();
      for (var n = 1; n <= 8; n++) { accounts[n-1] = {'Account':'', 'Password':''}; }
      await refreshHealth();
      for (var n = 1; n <= 8; n++) { accounts[n-1] = await service.loadAccount(n); }
      loadFields();
      report('已切换区服，账号、角色和商城数据相互独立');
    } catch (e) { await error(e); }
    finally { if (mounted) setState(() => busy = false); }
  }

  void loadFields() {
    account.text = accounts[selected - 1]['Account'] ?? '';
    password.text = accounts[selected - 1]['Password'] ?? '';
  }

  Future<void> save() async {
    if (!ready) return;
    await service.saveAccount(selected, account.text.trim(), password.text);
    accounts[selected - 1] = {
      'Account': account.text.trim(),
      'Password': password.text,
    };
    await writeAtomic(
      preferences.path,
      utf8.encode(jsonEncode({'frame_mode': frameMode.name, 'fps': fps, 'hide': hide})),
    );
  }

  Future<void> refreshHealth() async {
    try {
      final text = await service.health();
      if (mounted) setState(() => health = '服务器连接正常 · $text');
    } catch (e) {
      report('服务器检查失败：$e');
      if (mounted) setState(() => health = connectionFailureText(e));
    }
  }

  Future<void> error(Object e) async {
    if (!mounted) return;
    report(e is GameDirectoryError ? '请放到游戏目录下' : '操作未完成');
    await showDialog<void>(
      context: context,
      builder: (c) => AlertDialog(
        title: Text(e is GameDirectoryError ? '启动器提示' : '操作详情'),
        content: SizedBox(
          width: 640,
          child: SingleChildScrollView(child: SelectableText(publicError(e))),
        ),
        actions: [
          if (e is! GameDirectoryError)
            TextButton(
              onPressed: () =>
                  Clipboard.setData(ClipboardData(text: publicError(e))),
              child: const Text('复制详情'),
            ),
          TextButton(
            onPressed: () => Navigator.pop(c),
            child: const Text('关闭'),
          ),
        ],
      ),
    );
  }

  Future<bool> confirm(
    String title,
    String body, {
    String yes = '更新',
    String no = '取消',
  }) async =>
      await showDialog<bool>(
        context: context,
        builder: (c) => AlertDialog(
          title: Text(title),
          content: SizedBox(
            width: 600,
            child: SingleChildScrollView(child: Text(body)),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(c, false),
              child: Text(no),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(c, true),
              child: Text(yes),
            ),
          ],
        ),
      ) ??
      false;
  Future<void> requireUpdate(String title, String message) async {
    await showDialog<void>(context: context, barrierDismissible: false,
      builder: (c) => PopScope(canPop: false, child: AlertDialog(
        title: Text(title), content: SingleChildScrollView(child: Text('$message\n\n请关闭游戏,更新新版本！')),
        actions: [FilledButton(onPressed: () => Navigator.pop(c), child: const Text('立即更新'))],
      )));
  }

  Future<void> checkUpdate({bool manual = false}) async {
    updateBlocked = true;
    final previousBusy=busy;
    if(mounted)setState(()=>busy=true);
    try {
      if (ready && service.config['update_enabled'] == false) {
        updateBlocked = false;
        report('测试模式：已暂停在线更新。');
        await ensureGameDirectory();
        return;
      }
      if (manual && service.local) {
        updateBlocked = false;
        report('线下版本不检查在线更新。');
        await ensureGameDirectory();
        return;
      }
      release = await updates.check();
      updateBlocked = release != null;
      if (release == null && manual && mounted) {
        report('已是最新版！');
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('已是最新版！')),
        );
      }
      if (release == null) await ensureGameDirectory();
      if (release != null && mounted) {
        await requireUpdate('启动器必须更新', release!['notes'] ?? '发现新版本');
        await save();
        await updates.install(release!, report);
      }
    } catch (e) {
      await error(e);
    } finally {if(mounted)setState(()=>busy=previousBusy);}
  }

  final Set<String> confirmedLegacyDirectories = {};

  Future<bool> confirmClientDirectory(String directory) async {
    if ((!directory.contains('老登') && !await File(p.join(directory, 'gfld.dat')).exists()) ||
        confirmedLegacyDirectories.contains(directory)) return true;
    final accepted = await confirm('确认客户端版本',
      '检测到 gfld.dat 或目录名称含“老登”。请确认是否为原版客户端，建议不要使用“功夫老登端”。仅凭文件名无法判断客户端来源。',
      yes: '确认使用', no: '重新选择');
    if (accepted) confirmedLegacyDirectories.add(directory);
    return accepted;
  }

  Future<bool> ensureGameDirectory({bool choose = false}) async {
    if (!choose && await LauncherService.isGameDirectory(service.game)) {
      if (await confirmClientDirectory(service.game)) {
        await service.selectGameDirectory(service.game);
        if (mounted) setState(() => gameReady = true);
        return true;
      }
      choose = true;
    }
    if (mounted) setState(() => gameReady = false);
    var retry = choose || await confirm('请选择游戏目录',
      '当前目录不是完整的游戏目录。请选择包含 Data 文件夹和 gfxz.dat 或 gfld.dat 的目录。',
      yes: '选择目录', no: '暂不选择');
    while (retry && mounted) {
      final directory = await getDirectoryPath(confirmButtonText: '选择游戏目录');
      if (directory == null || !mounted) break;
      try {
        if (!await LauncherService.isGameDirectory(directory)) throw GameDirectoryError();
        if (!await confirmClientDirectory(directory)) { retry = true; continue; }
        await service.selectGameDirectory(directory);
        if (mounted) setState(() { gameReady = true; running.clear(); });
        await refreshRunning();
        report('游戏目录已选择：${service.game}');
        return true;
      } on GameDirectoryError {
        retry = await confirm('游戏目录不正确',
          '所选目录缺少 Data/config.spf2 或游戏程序（gfxz.dat / gfld.dat），无法启动游戏。是否重新选择？',
          yes: '重新选择', no: '取消');
      }
    }
    report('未选择有效游戏目录，无法启动游戏。请点击“设置游戏目录”。');
    return false;
  }

  Future<void> chooseGameDirectory() async {
    if (busy || !ready) return;
    setState(() => busy = true);
    try { await ensureGameDirectory(choose: true); }
    catch (e) { await error(e); }
    finally { if (mounted) setState(() => busy = false); }
  }

  Future<bool> resolvePortConflicts() async {
    for(var attempt=0; attempt<4; attempt++) {
      final conflicts=await service.portConflicts();
      if(conflicts.isEmpty) return true;
      final owner=conflicts.first;
      final same=conflicts.where((r)=>r['PID']==owner['PID']);
      final label='${owner['Name']}（PID ${owner['PID']}）';
      final ports=same.map((r)=>"${r['Protocol']} ${r['Port']}").join('、');
      if(owner['CanStop']!=true) throw Exception('$label 占用了本地端口 $ports。无法安全结束，请手动关闭；系统服务请联系管理员处理。');
      final yes=await confirm('本地端口被占用', '$label 占用了本地端口 $ports。\n可能是其他区服的游戏或旧登录组件。请先保存该程序中的工作。结束它可能使相关游戏断线。\n是否结束该程序并重新检查？',yes:'结束该程序',no:'取消启动');
      if(!yes) return false;
      await service.native({...owner,'Op':'stop_port_owner'});
      await Future<void>.delayed(const Duration(milliseconds:500));
    }
    throw Exception('仍有程序占用本地端口，请关闭相关程序后重试。');
  }

  Future<void> launch() async {
    setState(() { busy = true; transfer = null; });
    try {
      await checkUpdate();
      if (updateBlocked) return;
      if (!await ensureGameDirectory()) return;
      await save();
      await service.validate();
      final client = service.config['update_enabled'] == false ? null : await updates.clientCheck();
      if (client != null) {
        await requireUpdate('客户端必须更新', client['notes'] ?? '发现客户端更新');
        await updates.installClient(client, report);
      }
      if (!service.local && service.config['update_enabled'] != false && updates.usesOss) {
        if (client != null && await updates.clientCheck() != null) {
          throw Exception('更新后文件校验未通过，请重新检查更新。');
        }
        service.verifiedRelease = updates.verifiedRelease;
      }
      if (!await resolvePortConflicts()) return;
      await service.launch(selected, frameMode, fps, report);
    } catch (e) {
      await error(e);
    } finally {
      await refreshRunning();
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> select(int n) async {
    if (busy || !ready) return;
    try {
      await save();
      setState(() {
        selected = n;
        running.remove(n);
        loadFields();
      });
      await refreshRunning();
    } catch (e) {
      await error(e);
    }
  }

  Future<void> logs() async {
    try {
      final text=await service.logText();
      if(!mounted)return;
      await showDialog<void>(context:context,builder:(dialogContext)=>AlertDialog(
        title:Text('窗口 $selected 日志'),
        content:SizedBox(width:760,height:420,child:Column(crossAxisAlignment:CrossAxisAlignment.stretch,children:[
          const Text('预览最近 32 KB；导出 TXT 包含当前日志的完整内容。多开窗口共用网络日志。'),
          const SizedBox(height:12),Expanded(child:SingleChildScrollView(child:SelectableText(text))),
        ])),
        actions:[
          TextButton(onPressed:()=>Clipboard.setData(ClipboardData(text:text)),child:const Text('复制预览')),
          TextButton(onPressed:() async {
            try {
              final stamp=DateTime.now().toIso8601String().replaceAll(':','-').split('.').first;
              final username=account.text.trim().replaceAll(RegExp(r'[<>:"/\\|?*\x00-\x1f]'), '_');
              final prefix=username.isEmpty ? '' : '$username-';
              final location=await getSaveLocation(suggestedName:'$prefix窗口$selected-日志-$stamp.txt',acceptedTypeGroups:[const XTypeGroup(label:'TXT 文本',extensions:['txt'])]);
              if(location==null)return;
              final destination=location.path.toLowerCase().endsWith('.txt')?location.path:'${location.path}.txt';
              await exportLog(File(p.join(service.shared,'online-client.log')),destination);
              report('日志已导出：$destination');
              if(dialogContext.mounted)Navigator.pop(dialogContext);
            } catch(e) {await error(e);}
          },child:const Text('导出 TXT')),
          TextButton(onPressed:()=>Navigator.pop(dialogContext),child:const Text('关闭')),
        ]));
    } catch(e) {await error(e);}
  }

  @override
  void dispose() {
    stateTimer?.cancel();
    account.dispose();
    password.dispose();
    super.dispose();
  }

  Future<void> settings() async {
    await showDialog<void>(context: context, builder: (c) => StatefulBuilder(
      builder: (c, update) => AlertDialog(
        title: Text('设置 · 窗口 $selected'),
        content: SizedBox(width: 480, child: SingleChildScrollView(child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch, mainAxisSize: MainAxisSize.min,
          children: [
            const Text('游戏目录'), const SizedBox(height: 8),
            SelectableText(widget.preview ? '请选择游戏目录' : service.game, style: const TextStyle(fontSize: 12)),
            const SizedBox(height: 8),
            OutlinedButton.icon(icon: const Icon(Icons.folder_open), label: const Text('设置游戏目录'),
              onPressed: ready && !busy ? () async { Navigator.pop(c); await chooseGameDirectory(); } : null),

          ],
        ))),
        actions: [TextButton(onPressed: () => Navigator.pop(c), child: const Text('关闭')),
          FilledButton(onPressed: () async {
            try { if (!widget.preview) await save(); if (c.mounted) Navigator.pop(c); report('设置已保存'); }
            catch (e) { await error(e); }
          }, child: const Text('保存设置'))],
      ),
    ));
  }

  bool maximized = false;
  Future<void> windowAction(String action) async {
    try {
      final value=await const MethodChannel('launcher/window').invokeMethod<bool>(action);
      if(mounted && action=='maximize') setState(() => maximized=value ?? false);
    } catch(e) { await error(e); }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    body: ClassicBackdrop(
      child: SafeArea(child: Padding(padding: const EdgeInsets.fromLTRB(20, 10, 20, 16), child: Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(children: [
          TextButton(onPressed: busy ? null : () => checkUpdate(manual: true), child: const Text('检查更新', style: TextStyle(color: Color(0xffffdd57), fontWeight: FontWeight.bold))),
          TextButton(onPressed: busy ? null : settings, child: const Text('设置', style: TextStyle(color: Color(0xffffdd57), fontWeight: FontWeight.bold))),
          TextButton(onPressed: () => confirm('使用说明', '选择窗口后点击“进入游戏”。', yes: '知道了'), child: const Text('使用说明', style: TextStyle(color: Color(0xffffdd57), fontWeight: FontWeight.bold))),
          Expanded(child: GestureDetector(
            behavior: HitTestBehavior.opaque,
            onPanStart: (_) => windowAction('drag'),
            onDoubleTap: () => windowAction('maximize'),
            child: const SizedBox(height: 40))),
          IconButton(tooltip: '最小化', onPressed: () => windowAction('minimize'),
            icon: const Icon(Icons.remove, color: Color(0xffffdd57))),
          IconButton(tooltip: maximized ? '还原窗口' : '最大化', onPressed: () => windowAction('maximize'),
            icon: Icon(maximized ? Icons.filter_none : Icons.crop_square, size: 19, color: const Color(0xffffdd57))),
          IconButton(tooltip: '关闭启动器', hoverColor: Colors.red, onPressed: () => windowAction('close'),
            icon: const Icon(Icons.close, color: Color(0xffffdd57))),
        ]),
        const SizedBox(height: 8),
        Expanded(child: Container(
          decoration: BoxDecoration(color: Colors.white, borderRadius: BorderRadius.circular(6),
            border: Border.all(color: const Color(0xffffd8a5)),
            boxShadow: const [BoxShadow(color: Color(0x18000000), blurRadius: 12, offset: Offset(0, 4))]),
          child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
            Padding(padding: const EdgeInsets.fromLTRB(26, 14, 16, 8), child: Row(children: [
              const Text('更新公告', style: TextStyle(color: Color(0xff9b3825), fontWeight: FontWeight.bold, fontSize: 17)),
              const Spacer(),
              IconButton(onPressed: ready && !readingAnnouncement ? refreshAnnouncement : null,
                tooltip: '刷新公告', icon: const Icon(Icons.refresh, color: Color(0xff8e735d))),
            ])),
            const Divider(height: 1, color: Color(0xffdfd2c1)),
            Expanded(child: SingleChildScrollView(padding: const EdgeInsets.all(26), child: SelectableText(
              widget.preview ? '新增武器 · 王八拳拳谱\n\n感谢 QQ 512222607 制作。\n\n请务必更新后进入游戏。\n\n冰封阁楼双梯与启动器功能更新。' : announcement,
              style: const TextStyle(color: Color(0xff333333), fontSize: 16, height: 1.7),
            ))),
          ]),
        )),
        const SizedBox(height: 16),
        if (ready && service.realms.length > 1) ...[
          DropdownButtonFormField<String>(value: service.realmId, isExpanded: true,
            decoration: const InputDecoration(labelText: '选择区服', floatingLabelBehavior: FloatingLabelBehavior.never, border: OutlineInputBorder(), isDense: true),
            items: service.realms.map((r) => DropdownMenuItem<String>(value: r['id'] as String, child: Text(r['name'] as String))).toList(),
            onChanged: busy ? null : (id) { if (id != null) selectRealm(id); }),
          const SizedBox(height: 12),
        ],
        Row(children: [
          Expanded(child: TextField(controller: account, enabled: ready && !busy,
            textInputAction: TextInputAction.next,
            decoration: InputDecoration(labelText: '窗口 $selected · 账号', floatingLabelBehavior: FloatingLabelBehavior.never, border: const OutlineInputBorder(), isDense: true))),
          const SizedBox(width: 12),
          Expanded(child: TextField(controller: password, enabled: ready && !busy, obscureText: !hide,
            decoration: InputDecoration(labelText: '密码', floatingLabelBehavior: FloatingLabelBehavior.never, border: const OutlineInputBorder(), isDense: true,
              suffixIcon: IconButton(tooltip: hide ? '隐藏密码' : '显示密码',
                icon: Icon(hide ? Icons.visibility_off : Icons.visibility), onPressed: () => setState(() => hide = !hide))))),
          IconButton(tooltip: '保存账号密码', icon: const Icon(Icons.save_outlined), onPressed: ready && !busy ? () async {
            try { await save(); report('账号密码已保存'); } catch (e) { await error(e); }
          } : null),
        ]),
        const SizedBox(height: 6),
        const Text('账号不存在时，登录即自动注册。账号密码按区服和窗口记忆。', style: TextStyle(fontSize: 11, color: Color(0xffffdd57))),
        const SizedBox(height: 18),
        Row(children: [
          Expanded(child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            const Text('选择窗口', style: TextStyle(fontSize: 12, color: Color(0xffffdd57))),
            const SizedBox(height: 8),
            Wrap(spacing: 6, runSpacing: 6, children: List.generate(8, (index) => SizedBox(width: 46, height: 39,
              child: OutlinedButton(style: OutlinedButton.styleFrom(padding: EdgeInsets.zero,
                backgroundColor: selected == index + 1 ? const Color(0xffffd438) : const Color(0xfffff9ec),
                foregroundColor: selected == index + 1 ? const Color(0xff332416) : const Color(0xff68361b),
                shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(6))),
                onPressed: busy ? null : () { if (widget.preview) { setState(() => selected = index + 1); } else { select(index + 1); } },
                child: Text('${index + 1}')))),
            ),
          ])),
          const SizedBox(width: 16),
          SizedBox(height: 57, width: 216, child: Semantics(button: true,
            label: running[selected] == true ? '已启动' : '进入游戏', child: Tooltip(message: '进入游戏',
              child: InkWell(onTap: ready && gameReady && !updateBlocked && !busy && running[selected] == false ? launch : null,
                child: Stack(fit: StackFit.expand, children: [
                  Image.asset('assets/classic/start.png', fit: BoxFit.fill),
                  if (running[selected] == true) const ColoredBox(color: Color(0xfff5ce25),
                    child: Center(child: Text('已启动', style: TextStyle(color: Colors.black, fontSize: 21)))),
                ]))))),
        ]),
        const SizedBox(height: 18),
        Row(children: [
          Expanded(child: DropdownButtonFormField<FrameMode>(value: frameMode, isExpanded: true,
            decoration: const InputDecoration(labelText: '帧率模式 · 重启游戏生效', floatingLabelBehavior: FloatingLabelBehavior.never, border: OutlineInputBorder(), isDense: true),
            items: const [
              DropdownMenuItem(value: FrameMode.normal, child: Text('普通模式')),
              DropdownMenuItem(value: FrameMode.high125, child: Text('高帧率模式 1 · 约 125 FPS')),
              DropdownMenuItem(value: FrameMode.configZero, child: Text('高帧率模式 2 · 最高约 500帧（实验）')),
            ], onChanged: busy ? null : (v) { if(v != null) setState(() => frameMode = v); })),
          const SizedBox(width: 12),
          Switch(value: fps, onChanged: busy ? null : (v) => setState(() => fps = v)), const Text('显示 FPS', style: TextStyle(color: Color(0xffffdd57))),
          const SizedBox(width: 8),
          IconButton(onPressed: ready && !busy ? () => service.show(selected) : null,
            tooltip: '显示游戏窗口', icon: const Icon(Icons.open_in_new)),
        ]),
        const SizedBox(height: 10),
        Row(children: [const Icon(Icons.public, size: 15, color: Color(0xffffdd57)), const SizedBox(width: 8),
          Expanded(child: Text(health, maxLines: 2, style: const TextStyle(fontSize: 12, color: Color(0xffffdd57)))),
          IconButton(onPressed: ready ? refreshHealth : null, tooltip: '刷新服务器状态', icon: const Icon(Icons.refresh, size: 17)),
        ]),
        Row(children: [
          Expanded(child: ClassicProgress(value: transfer?.overallFraction)),
          const SizedBox(width: 16),
          Text(transfer == null ? '准备就绪' : '${((transfer!.overallFraction ?? 0) * 100).round()}%',
            style: const TextStyle(color: Color(0xffffdd57))),
        ]),
        if (transfer != null) UpdateProgressView(transfer!),
        if (busy && transfer == null) const LinearProgressIndicator(),
        Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Expanded(child: SelectableText(status, maxLines: 2, style: const TextStyle(fontSize: 12, color: Colors.white))),
          const SizedBox(width: 12),
          const Text(launcherDisplayVersion, style: TextStyle(fontSize: 12, color: Color(0xffffdd57))),
        ]),
      ],
    )))),
  );
}
