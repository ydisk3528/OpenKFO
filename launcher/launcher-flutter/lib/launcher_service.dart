import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:path/path.dart' as p;
import 'frame_mode.dart';
import 'connection_error.dart';
import 'embedded_certificates.dart';

String hashBytes(List<int> bytes) => sha256.convert(bytes).toString();
Future<String> fileHash(String path) async =>
    (await sha256.bind(File(path).openRead()).first).toString();
Future<void> writeAtomic(String path, List<int> bytes) async {
  final file = File(path);
  await file.parent.create(recursive: true);
  final temp = File('$path.${DateTime.now().microsecondsSinceEpoch}.tmp');
  try {
    await temp.writeAsBytes(bytes, flush: true);
    await temp.rename(path);
  } finally {
    if (await temp.exists()) await temp.delete();
  }
}

String publicError(Object error) => error.toString()
    .replaceAll(RegExp(r'(?:https?|wss?|tls|tcp|udp)://[^\s"<>]+', caseSensitive: false), '[连接地址]')
    .replaceAll(RegExp(r'\b(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?\b'), '[网络地址]')
    .replaceAll(RegExp(r'\[[0-9a-fA-F:%]+\](?::[0-9]+)?'), '[网络地址]')
    .replaceAllMapped(RegExp(r'\b(?:[a-z0-9-]+\.)+[a-z]{2,63}(?::\d+)?\b', caseSensitive: false), (m) {
      final value = m[0]!;
      if (RegExp(r'\.(exe|dll|dat|log|json|xml|crt|pem)$', caseSensitive: false).hasMatch(value)) return value;
      return '[连接地址]';
    });

class GameDirectoryError implements Exception {
  @override
  String toString() => '请选择完整的游戏目录。该目录需要包含 Data/config.spf2，以及 gfxz.dat 或 gfld.dat。';
}

// Off by default: empty keeps the original OSS-only behaviour. Enable at build
// time with --dart-define=SERVER_VERSION_URL=https://<host>/dl/version/version.json
const serverVersionUrl = String.fromEnvironment('SERVER_VERSION_URL');
const legacyClientHash = '98c43be72ac7600b368d4e185d75205376f79e938ea42e4b16ce8f8c4bae827b';

class LauncherService {
 String? verifiedRelease;
  final String root;
  late Map<String, dynamic> config;
  late Map<String, dynamic> components;
  List<Map<String, dynamic>> realms = [];
  Map<String, dynamic> _baseConfig = {};
  String realmId = '';
  File get realmSelection => File(p.join(root, 'realm-selection.json'));

  Future<void> loadRealms() async {
    _baseConfig = Map<String, dynamic>.from(config);
    if (p.basename(configPath) == 'bridge.local.json' || !components.containsKey('realms.json')) return;
    final data = jsonDecode(utf8.decode(await component('realms.json')));
    final ids = <String>{};
    for (final raw in data as List) {
      final r = Map<String, dynamic>.from(raw as Map);
      final uri = Uri.tryParse(r['url'] as String? ?? '');
      // Dart treats WSS as an unknown scheme (default port 0); HTTPS has the same wire port.
      final port = uri?.scheme == 'wss'
          ? Uri.tryParse((r['url'] as String).replaceFirst('wss:', 'https:'))?.port
          : uri?.port;
      if (r['id'] is! String || !RegExp(r'^[a-z0-9-]+$').hasMatch(r['id']) || !ids.add(r['id']) ||
          r['name'] is! String || (r['name'] as String).isEmpty || uri == null || !['tls', 'wss'].contains(uri.scheme) ||
          uri.host.isEmpty || (uri.scheme == 'tls' && !uri.hasPort) || (port == null || port < 1 || port > 65535) || uri.hasQuery || uri.hasFragment || uri.userInfo.isNotEmpty ||
          r['server_certificate'] is! String || !(components.containsKey(r['server_certificate']) ||
              (config['embedded_certificates'] == true && embeddedCertificates.containsKey(r['server_certificate'])))) {
        throw Exception('区服配置无效，请重新下载完整启动器。');
      }
      realms.add(r);
    }
    if (realms.isEmpty) throw Exception('区服列表为空');
    String? saved;
    try { saved = (jsonDecode(await realmSelection.readAsString()) as Map)['id'] as String?; }
    on FileSystemException { /* First start. */ }
    on FormatException { /* Invalid preference uses the first realm. */ }
    final chosen = realms.firstWhere((r) => r['id'] == saved, orElse: () => realms.first);
    await _applyRealm(chosen);
  }

  Future<void> _applyRealm(Map<String, dynamic> r) async {
    final certificate = r['server_certificate'] as String;
    if (config['embedded_certificates'] != true) await component(certificate);
    else if (!embeddedCertificates.containsKey(certificate)) throw Exception('内置证书不匹配，请更新完整启动器。');
    config = Map<String, dynamic>.from(_baseConfig)
      ..['url'] = r['url']
      ..['server_certificate'] = p.join('launcher-files', certificate);
    final updateUrl = r['launcher_update_version_url'];
    if (updateUrl != null) {
      final uri = Uri.tryParse(updateUrl as String);
      if (uri == null || uri.scheme != 'https' || uri.host.isEmpty || uri.userInfo.isNotEmpty) {
        throw Exception('区服更新地址无效，请重新下载完整启动器。');
      }
      config['launcher_update_version_url'] = updateUrl;
    }
    if (realms.length == 1 || r['id'] != realms.first['id']) config['credentials_scope'] = 'realm:${r['id']}:${r['url']}';
    realmId = r['id'];
    verifiedRelease = null;
  }

  Future<void> selectRealm(String id) async {
    if (id == realmId) return;
    final r = realms.firstWhere((r) => r['id'] == id);
    for (var n = 1; n <= 8; n++) {
      if (await state(n) != null) throw Exception('切换区服前，请先关闭所有游戏窗口。');
    }
    if (await liveBridgeOwner() != null) throw Exception('登录组件仍在退出，请关闭游戏后等待约30秒，再切换区服。');
    await _applyRealm(r);
    await writeAtomic(realmSelection.path, utf8.encode(jsonEncode({'id': realmId})));
  }

  late String game;
  String credentialsKey = 'LS1KuGmfVgqfuRT2';
  LauncherService(this.root);
  List<int> localPorts = [38184, 38180, 38181];
  bool autoPorts = false;
  File get portSettingsFile => File(p.join(root, 'local-ports.json'));
  static void validatePorts(List<int> ports) {
    if (ports.length != 3 || ports.any((v) => v < 1 || v > 65535) || ports.toSet().length != 3) {
      throw const FormatException('请填写三个不同的端口，范围为 1–65535。');
    }
  }
  Future<void> loadPortSettings() async {
    if (!await portSettingsFile.exists()) return;
    final data = jsonDecode(await portSettingsFile.readAsString()) as Map;
    final ports = (data['ports'] as List).cast<int>();
    validatePorts(ports);
    localPorts = ports;
    autoPorts = data['automatic'] == true;
  }
  Future<void> savePortSettings(List<int> ports, bool automatic) async {
    validatePorts(ports);
    for (var n = 1; n <= 8; n++) {
      if (await state(n) != null) throw StateError('请先关闭所有游戏窗口，再更改端口。');
    }
    if (await liveBridgeOwner() != null) throw StateError('登录组件仍在退出，请关闭游戏后等待约30秒再更改端口。');
    await writeAtomic(portSettingsFile.path, utf8.encode(jsonEncode({'ports': ports, 'automatic': automatic})));
    localPorts = List<int>.from(ports);
    autoPorts = automatic;
  }
  Future<Map<String,dynamic>?> liveBridgeOwner() async {
    try {
      final data = jsonDecode(await File(p.join(shared,'bridge-owner.json')).readAsString());
      if (data is! Map<String,dynamic> || data['PID'] is! int || data['Created'] is! int) return null;
      final owner = data;
      final alive = await native({...owner, 'Op':'window', 'Action':'status', 'Image':bridgeExecutable});
      if (alive == true) return {...owner,'Compatible':true};
      // A previous component build can still consume shared start requests.
      // Identify its lifetime before treating the owner marker as stale.
      try {
        final created = await native({'Op':'info','PID':owner['PID']});
        if (created == owner['Created']) return {...owner,'Compatible':false};
      } on Exception { /* Exited owner; PID may no longer exist. */ }
      return null;
    } on FileSystemException { return null; }
      on FormatException { return null; }
  }
  String get payload => p.join(root, 'launcher-files');
  String get support => p.join(root, 'LauncherSupport.exe');
  Future<String> fpsExecutable() async {
    final bytes = await File(support).readAsBytes();
    final hash = hashBytes(bytes);
    final target = p.join(game, 'launcher-components', 'fps', hash, 'LauncherSupport.exe');
    if (!await File(target).exists() || await fileHash(target) != hash) {
      await writeAtomic(target, bytes);
    }
    return target;
  }
  String get shared => p.join(game, 'launcher-components', 'shared');
  String get bridgeExecutable => p.join(
    shared, 'versions', components['OnlineBridge.exe'] as String, 'OnlineBridge.exe',
  );
  String get clientExecutable {
    // Select from the actual game directory, including installations with an
    // older bridge.json that still pins gfld.dat. Hash validation stays in prepare.
    return File(p.join(game, 'gfxz.dat')).existsSync() ? 'gfxz.dat' : 'gfld.dat';
  }
  Uri get endpoint => Uri.parse(config['url'] as String);
  bool get local => ['127.0.0.1', 'localhost', '::1'].contains(endpoint.host);
  String resolve(String name) => p.normalize(p.join(root, name));
  String get configPath {
    final name = p.basename(Platform.resolvedExecutable);
    return p.join(root, name.contains('线下') || name.contains('本地')
        ? 'bridge.local.json' : 'bridge.json');
  }
  Future<dynamic> native(Map<String, dynamic> request) async {
    final process = await Process.start(support, [], workingDirectory: root);
    return nativeReply(process, request);
  }

  static Future<dynamic> nativeReply(Process process, Map<String, dynamic> request,
      {Duration timeout = const Duration(seconds: 15)}) async {
    // Drain both pipes immediately, including while sending the request.
    final output = utf8.decoder.bind(process.stdout).join();
    final errors = utf8.decoder.bind(process.stderr).join();
    Future<int> send() async {
      process.stdin.write(jsonEncode(request));
      await process.stdin.close();
      return 0;
    }
    late List<Object> reply;
    try {
      reply = await Future.wait<Object>([output, errors, process.exitCode, send()], eagerError: true).timeout(timeout);
    } on TimeoutException {
      throw TimeoutException('本地辅助组件响应超时，请重试；仍失败请检查完整启动器和安全软件。');
    } finally {
      process.kill();
    }
    final text = reply[0] as String;
    if (reply[2] != 0) throw Exception('本地辅助组件执行失败');
    final response = jsonDecode(text) as Map<String, dynamic>;
    if (response['ok'] != true) throw Exception(response['error']);
    return response['result'];
  }

  Future<void> init() async {
    await loadPortSettings();
    components = jsonDecode(
      await File(p.join(payload, 'files.json')).readAsString(),
    );
    for (final name in [
      'bridge.json',
    ]) {
      final target = p.join(root, name);
      if (!await File(target).exists()) {
        await writeAtomic(target, await component(name));
      }
    }
    config = jsonDecode(await File(configPath).readAsString());
    // Upgrade existing online settings without relying on loose certificate files.
    if (config['embedded_certificates'] != false && !local && ['server_certificate','login_certificate','login_key'].every((key) =>
        embeddedCertificates.containsKey((config[key] as String? ?? '').replaceAll('\\', '/').replaceFirst(RegExp(r'^launcher-files/'), '')))) {
      config['embedded_certificates'] = true;
    }
    if (config['embedded_certificates'] != true) {
      for (final name in ['launcher-certificates/online/origin.crt','launcher-certificates/online/login.crt','launcher-certificates/online/login.key']) {
        if (components.containsKey(name) && !await File(p.join(root,name)).exists()) {
          await writeAtomic(p.join(root,name), await component(name));
        }
      }
    }
    config.putIfAbsent('update_version_url', () => 'https://openkfo.oss-cn-hangzhou.aliyuncs.com/version/version.json');
    // When enabled, the game-server mirror is tried first and OSS is the fallback.
    if (serverVersionUrl.isNotEmpty) {
      config.putIfAbsent('update_version_urls', () => [serverVersionUrl]);
    }
    await loadRealms();
    game = p.normalize(
      p.absolute(root, config['client_directory'] as String? ?? '.'),
    );
    await restoreGameDirectory();
    if (config['shared_client'] != true) {
      throw Exception('这个版本需要共享客户端配置，请使用配套完整启动器 ZIP。');
    }
  }

  Future<List<int>> component(String name) async {
    final bytes = await File(p.join(payload, name)).readAsBytes();
    if (components[name] != hashBytes(bytes)) {
      throw Exception('组件校验失败，请重新解压完整启动器 ZIP：$name');
    }
    return bytes;
  }

  File get gameDirectoryFile => File(p.join(root,
      p.basename(configPath) == 'bridge.local.json'
          ? 'game-directory.local.json' : 'game-directory.json'));

  static Future<bool> isGameDirectory(String directory) async {
    Future<bool> present(String name) async {
      try {
        final stat = await File(p.join(directory, name)).stat();
        return stat.type == FileSystemEntityType.file && stat.size > 0;
      } on FileSystemException { return false; }
    }
    return await present(p.join('Data', 'config.spf2')) &&
        (await present('gfxz.dat') || await present('gfld.dat'));
  }

  Future<void> restoreGameDirectory() async {
    if (!await gameDirectoryFile.exists()) return;
    try {
      final saved = jsonDecode(await gameDirectoryFile.readAsString());
      if (saved is Map && saved['path'] is String && p.isAbsolute(saved['path'])) {
        game = p.normalize(saved['path'] as String);
      }
    } on FormatException {
      // Invalid saved selection falls back to the configured directory.
    }
  }

  Future<void> selectGameDirectory(String directory) async {
    final selected = p.normalize(p.absolute(directory));
    if (!await isGameDirectory(selected)) throw GameDirectoryError();
    await writeAtomic(gameDirectoryFile.path, utf8.encode(jsonEncode({'path': selected})));
    game = selected;
  }

  Future<void> validate() async {
    if (!await isGameDirectory(game)) throw GameDirectoryError();
  }

  String credentialsPath(int number) {
    var scope = config['credentials_scope'] as String? ?? endpoint.toString();
    if (endpoint.path.isEmpty && config['credentials_scope'] == null) {
      scope = '$scope/';
    }
    return p.join(
      Platform.environment['LOCALAPPDATA']!,
      'OpenKFO',
      'Launcher',
      hashBytes(utf8.encode(scope)).substring(0, 16),
      'window-$number',
      'credentials.bin',
    );
  }

  Future<Map<String, dynamic>> loadAccount(int n) async {
    final dest = credentialsPath(n);
    if (!await File(dest).exists()) {
      final old = p.join(
        root,
        'launcher-components',
        'window-$n',
        'credentials.bin',
      );
      if ((realms.isEmpty || realmId == realms.first['id']) && await File(old).exists()) {
        final value = Map<String, dynamic>.from(
          await native({'Op': 'load', 'Path': old}),
        );
        await saveAccount(n, value['Account'] ?? '', value['Password'] ?? '');
        // Keep legacy record until the new release has been accepted in live testing.
      }
    }
    return Map<String, dynamic>.from(
      await native({'Op': 'load', 'Path': dest}),
    );
  }

  Future<void> saveAccount(int n, String account, String password) async =>
      await native({
        'Op': 'save',
        'Path': credentialsPath(n),
        'Account': account,
        'Password': password,
        'Key': credentialsKey,
      });
  Future<Map<String, dynamic>?> state(int n) async {
    try {
      final state = jsonDecode(
        await File(p.join(shared, 'window-$n.json')).readAsString(),
      ) as Map<String, dynamic>;
      final alive = await native({
        'Op': 'window',
        'Action': 'status',
        'PID': state['PID'],
        'Created': state['Created'],
        'Image': p.join(game, clientExecutable),
      });
      return alive == true ? state : null;
    } on FileSystemException {
      return null;
    } on FormatException {
      return null;
    }
  }

  Future<void> show(int n) async {
    final s = await state(n);
    if (s != null) {
      await native({
        ...s,
        'Op': 'window',
        'Action': 'show',
        'Image': p.join(game, clientExecutable),
      });
    }
  }

  Future<List<int>> verifiedCertificate(String key) async {
    final name=(config[key] as String).replaceAll('\\','/');
    final bundled=name.startsWith('launcher-files/') ? name.substring(15) : name;
    if (config['embedded_certificates'] == true) {
      final encoded = embeddedCertificates[bundled];
      if (encoded == null) throw Exception('内置证书不匹配，请更新完整启动器。');
      return base64Decode(encoded);
    }
    if (components.containsKey(bundled)) return component(bundled);
    if (local) return File(resolve(name)).readAsBytes();
    throw Exception('配套证书缺失或配置不匹配，请重新解压完整启动器；不要复制旧版证书。');
  }
  Future<String> connectionScope() async => hashBytes(utf8.encode(jsonEncode([
    endpoint.toString(),config['config_hash'],p.normalize(p.join(game,clientExecutable)).toLowerCase(),
    localPorts, autoPorts,
    for(final key in ['server_certificate','login_certificate','login_key']) hashBytes(await verifiedCertificate(key)),
  ])));
  Future<List<Map<String,dynamic>>> portConflicts() async {
    final live = await liveBridgeOwner();
    if (live != null && (live['Compatible'] != true || live['Scope'] != await connectionScope())) {
      throw StateError('现有登录组件配置不同，请关闭游戏并等待组件退出后再启动。');
    }
    if (autoPorts || live != null) return [];
    await enableAutomaticPortsIfUnavailable();
    return [];
  }
  Future<void> enableAutomaticPortsIfUnavailable() async {
    if (autoPorts) return;
    final sockets = <ServerSocket>[];
    RawDatagramSocket? udp;
    var unavailable = false;
    try {
      for (final port in localPorts) {
        sockets.add(await ServerSocket.bind(InternetAddress.loopbackIPv4, port, shared: false));
      }
      udp = await RawDatagramSocket.bind(InternetAddress.loopbackIPv4, localPorts[2], reuseAddress: false);
    } on SocketException {
      unavailable = true;
    } finally {
      udp?.close();
      for (final socket in sockets) { await socket.close(); }
    }
    if (unavailable) await savePortSettings(localPorts, true);
  }
  Future<void> restoreCertificates() async {
    if (config['embedded_certificates'] == true) {
      for (final key in ['server_certificate','login_certificate','login_key']) { await verifiedCertificate(key); }
      return;
    }
    for(final key in ['server_certificate','login_certificate','login_key']) {
      final bytes=await verifiedCertificate(key), target=resolve(config[key]);
      if (!await File(target).exists() || await fileHash(target)!=hashBytes(bytes)) await writeAtomic(target,bytes);
    }
  }
  Future<String> health() async {
    for (var attempt=0; attempt<2; attempt++) {
      try { return await checkHealth(); }
      on TimeoutException {
        if(attempt==1) rethrow;
      }
      on SocketException {
        if(attempt==1) rethrow;
      }
      await Future<void>.delayed(const Duration(milliseconds:500));
    }
    throw StateError('连接检查未完成');
  }
  Future<String> checkHealth() async {
    final watch = Stopwatch()..start();
    if (endpoint.scheme == 'wss') {
      final client = HttpClient()..connectionTimeout = const Duration(seconds: 8);
      try {
        final request = await client.getUrl(endpoint.replace(scheme: 'https', path: '/health', query: null))
            .timeout(const Duration(seconds: 8));
        request.followRedirects = false;
        final response = await request.close().timeout(const Duration(seconds: 8));
        if (response.statusCode != 200) throw await serviceHttpFailure(response, '服务器检查');
        final data = jsonDecode(await response.transform(utf8.decoder).join().timeout(const Duration(seconds: 8)));
        if (data['status'] != 'ok' || data['service'] != 'kungfu-go') throw Exception('服务器状态异常');
        final key = data['launcher_credentials_key'];
        if (key is String && RegExp(r'^[!-~]{16}$').hasMatch(key)) credentialsKey = key;
        return '请求往返 ${watch.elapsedMilliseconds} ms';
      } finally { client.close(force: true); }
    }
    if (endpoint.scheme != 'tls') throw Exception('此版本使用直连服务器，请检查配套配置。');
    final pem = utf8.decode(await verifiedCertificate('server_certificate'));
    final context=SecurityContext(withTrustedRoots:false)..setTrustedCertificatesBytes(utf8.encode(pem));
    final raw=await Socket.connect(endpoint.host,endpoint.port,timeout:const Duration(seconds:8));
    late SecureSocket socket;
    try {socket=await SecureSocket.secure(raw,host:'kk-origin',context:context).timeout(const Duration(seconds:8));}
    catch (_) {raw.destroy();rethrow;}
    try {
      final connect = watch.elapsedMilliseconds;
      watch.reset();
      socket.write('{"op":"health"}\n');
      await socket.flush();
      final line = await socket
          .cast<List<int>>().transform(utf8.decoder)
          .transform(const LineSplitter())
          .first
          .timeout(const Duration(seconds: 8));
      final data = jsonDecode(line);
      if (data['op'] != 'health' || data['value'] != 1) {
        throw Exception('服务器状态异常');
      }
      final key = data['launcher_credentials_key'];
      if (key is String && RegExp(r'^[!-~]{16}$').hasMatch(key)) {
        credentialsKey = key;
      }
      return '请求往返 ${watch.elapsedMilliseconds} ms · 连接准备 $connect ms';
    } finally {
      socket.destroy();
    }
  }

  Future<void> install(
    String name,
    String destination, {
    bool backup = true,
  }) async {
    final bytes = await component(name);
    final file = File(destination);
    if (await file.exists()) {
      final old = await fileHash(destination);
      if (old == hashBytes(bytes)) return;
      if (backup) {
        final copy = File(
          p.join(
            game,
            'launcher-components',
            'backups',
            old,
            p.basename(destination),
          ),
        );
        if (!await copy.exists()) {
          await copy.parent.create(recursive: true);
          await file.copy(copy.path);
        }
      }
    }
    try {
      await writeAtomic(destination, bytes);
    } on FileSystemException {
      throw Exception('组件正在使用，原文件已保留。请关闭游戏后重试：${p.basename(destination)}');
    }
  }

  Future<void> validateClientExecutable() async {
    if (clientExecutable == 'gfld.dat') {
      final image = File(p.join(game, clientExecutable));
      if (!await image.exists()) {
        throw Exception('游戏目录缺少 gfxz.dat 或 gfld.dat，请将启动器放到完整游戏目录。');
      }
      if (await fileHash(image.path) != legacyClientHash) {
        throw Exception('gfld.dat 版本尚未适配，原文件未修改。');
      }
    } else {
      final image = File(p.join(game, clientExecutable));
      if (!await image.exists()) {
        throw Exception('游戏目录缺少 gfxz.dat，请放入对应客户端。');
      }
      if (await fileHash(image.path) !=
          '1b7c8676e778c7bd47f55184f927338da3cf70c6aa5f0beb59869ea0f0ac9d4e') {
        throw Exception('gfxz.dat 版本尚未适配，原文件未修改。');
      }
    }
  }

  String? keySettingsGame;
  Future<void>? keySettingsCapture;
  File get keySettingsFile => File(p.join(root, 'LoginKeySetting.xml'));

  String keySettingsBlock(String text) {
    final blocks = RegExp(r'<OperationsSettings>.*?</OperationsSettings>', dotAll: true).allMatches(text).toList();
    if (blocks.length != 1) throw StateError('按键配置不完整，已保留上次保存的按键。');
    final block = blocks.single.group(0)!;
    var body = block.substring('<OperationsSettings>'.length, block.length - '</OperationsSettings>'.length);
    for (final name in ['Up','Down','Left','Right','Aim','LAttack','WAttack','Jump','Defence','Skill','ConsumeWeaopon1','ConsumeWeaopon2','SwitchWeapon','Burst']) {
      final re = RegExp('<$name Key="([0-9]+)"></$name>');
      final matches = re.allMatches(body).toList();
      if (matches.length != 1 || int.parse(matches.single.group(1)!) > 255) {
        throw StateError('按键配置异常，已保留上次保存的按键。');
      }
      body = body.replaceFirst(re, '');
    }
    if (body.trim().isNotEmpty) throw StateError('按键配置格式不支持，未覆盖保存文件。');
    return block;
  }

  Future<void> restoreKeySettings() async {
    await keySettingsCapture;
    final current = File(p.join(game, 'Settings.xml'));
    final text = latin1.decode(await current.readAsBytes());
    final block = keySettingsBlock(text);
    if (!await keySettingsFile.exists()) {
      await writeAtomic(keySettingsFile.path, latin1.encode(block));
      return;
    }
    final saved = keySettingsBlock(latin1.decode(await keySettingsFile.readAsBytes()));
    await updatePlayerSettings((original) => original.replaceFirst(keySettingsBlock(original), saved));
  }

  Future<void> captureKeySettings() {
    return keySettingsCapture ??= _captureKeySettings().whenComplete(() => keySettingsCapture = null);
  }

  Future<void> _captureKeySettings() async {
    if (keySettingsGame != game) return; // Do not replace the saved keys before startup restoration.
    final current = File(p.join(game, 'Settings.xml'));
    final bytes = await current.readAsBytes();
    final block = keySettingsBlock(latin1.decode(bytes));
    final saved = await keySettingsFile.exists() ? keySettingsBlock(latin1.decode(await keySettingsFile.readAsBytes())) : null;
    if (saved == block) return;
    if (await fileHash(current.path) != hashBytes(bytes)) return;
    await writeAtomic(keySettingsFile.path, latin1.encode(block));
  }

  // Settings.xml belongs to the player. Preserve its original encoding and
  // keep each distinct version, including launches that need no modification.
  Future<void> updatePlayerSettings(String Function(String) transform) async {
    final settings = File(p.join(game, 'Settings.xml'));
    final original = await settings.readAsBytes();
    final text = latin1.decode(original);
    final updated = transform(text);
    if (updated != text) {
      for (var n = 1; n <= 8; n++) {
        if (await state(n) != null) {
          throw StateError('游戏窗口仍在运行，未改写 Settings.xml。请关闭该目录的游戏窗口后再更改帧率或选区设置。');
        }
      }
    }
    final digest = hashBytes(original);
    final backup = File(p.join(game, 'launcher-components', 'backups', digest, 'Settings.xml'));
    if (!await backup.exists()) await writeAtomic(backup.path, original);
    if (updated == text) return;
    if (await fileHash(settings.path) != digest) {
      throw StateError('Settings.xml 正被其他程序修改，本次未覆盖，请稍后重试。');
    }
    await writeAtomic(settings.path, latin1.encode(updated));
  }

  Future<void> prepare() async {
    await validate();
    await validateClientExecutable();
    for (final name in ['SDError.dll','lqbz.dll', 'libssl-1_1.dll', 'libcrypto-1_1.dll']) {
      await install(name, p.join(game, name));
    }
    // Private 32-bit runtime for the native login DLLs; no system installation.
    for (final name in components.keys.cast<String>().where(
      (name) => name.startsWith('runtime-x86/') &&
          name.endsWith('.dll') && name.split('/').length == 2,
    )) {
      await install(name, p.join(game, p.basename(name)));
    }
    final cert = await verifiedCertificate('login_certificate');
    await writeAtomic(p.join(game, 'zz.crt'), cert);
    // The bridge writes actual bound ports before starting the game. Do not
    // overwrite an active bridge's SDK configuration when opening more windows.
    if (await liveBridgeOwner() == null) {
      await install('client-config.xml', p.join(game, 'Data', 'config.xml'));
    }
    final settings = File(p.join(game, 'Settings.xml'));
    if (await settings.exists()) {
      await updatePlayerSettings((original) {
        final re = RegExp(r'(<LoginServer\b[^>]*\bIndex\s*=\s*")[^"]*(")');
        if (!re.hasMatch(original)) {
          throw Exception('Settings.xml 缺少选区设置，未修改其他设置');
        }
        return original.replaceAllMapped(re, (m) => '${m[1]}0${m[2]}');
      });
    }
    await Directory(shared).create(recursive: true);
    await install('OnlineBridge.exe', bridgeExecutable);
    if (components.containsKey('GameMod.exe')) {
      await install('GameMod.exe', p.join(p.dirname(bridgeExecutable), 'GameMod.exe'));
    }
  }

  Future<void> launch(
    int n,
    FrameMode frameMode,
    bool fps,
    void Function(String) status, {
    bool autoStartMod = false,
  }) async {
    await validate();
    final lock = await File(p.join(game, '.launcher-start.lock'))
        .open(mode: FileMode.append);
    try {
      await lock.lock(FileLock.exclusive);
      if ((await portConflicts()).isNotEmpty) throw Exception('本地端口占用情况已变化，请再次点击启动游戏重新检查。');
      if (await liveBridgeOwner() == null) {
        for (var number=1; number<=8; number++) {
          if (await state(number) != null) throw StateError('游戏仍由旧版组件运行，请先退出游戏，再使用新的端口设置启动。');
        }
      }
      await restoreCertificates();
      await prepare();
      var s = await state(n);
      if (s == null) {
        await restoreKeySettings();
        await updatePlayerSettings((original) => applyFrameMode(original, FrameMode.high125));
        final c = Map<String, dynamic>.from(config)
          ..addAll({
            'client_directory': game,
            'launcher_scope': await connectionScope(),
            'client_release': verifiedRelease ?? const String.fromEnvironment('LAUNCHER_VERSION', defaultValue: 'development'),
            'client_executable': clientExecutable,
            'client_sha256': await fileHash(p.join(game, clientExecutable)),
            'login_port': localPorts[0],
            'sdk_port': localPorts[1],
            'game_port': localPorts[2],
            'auto_ports': autoPorts,
            'control_directory': shared,
          });
        for (final key in [
          'server_certificate',
          'login_certificate',
          'login_key',
        ]) {
          c[key] = config['embedded_certificates'] == true ? config[key] : resolve(config[key]);
        }
        final path = p.join(
          root,
          'launcher-components',
          'window-$n',
          'bridge.json',
        );
        await writeAtomic(path, utf8.encode(jsonEncode(c)));
        await writeAtomic(
          p.join(shared, 'performance-$n.json'),
          utf8.encode(jsonEncode({'high_frame_rate': true, 'unlimited_frame_rate': false, 'show_fps': fps})),
        );
        await Process.start(
          bridgeExecutable,
          ['-config', path, '-window', '$n'],
          mode: ProcessStartMode.detached,
          workingDirectory: shared,
        );
        status('窗口 $n 正在启动游戏…');
        for (var i = 0; i < 60; i++) {
          await Future<void>.delayed(const Duration(milliseconds: 500));
          s = await state(n);
          if (s != null) break;
        }
        if (s == null) throw Exception('窗口 $n 启动超时\n${await logText()}');
      }
      keySettingsGame = game;
      final skinHash =
          (components['LoginSkin.dll'] as String).substring(0, 12) +
          (components['LoginSkinHost.exe'] as String).substring(0, 12);
      final skin = p.join(game, 'launcher-components', 'login-skin', skinHash);
      for (final name in ['LoginSkin.dll', 'LoginSkinHost.exe']) {
        await install(name, p.join(skin, name));
      }
      await Process.start(
        p.join(skin, 'LoginSkinHost.exe'),
        [
          '${s['PID']}',
          p.join(game, clientExecutable),
          p.join(skin, 'LoginSkin.dll'),
        ],
        mode: ProcessStartMode.detached,
        workingDirectory: skin,
      );
      if (autoStartMod && components.containsKey('GameMod.exe')) {
        await Process.start(
          p.join(p.dirname(bridgeExecutable), 'GameMod.exe'),
          ['--attach', '${s['PID']}', '${s['Created']}'],
          mode: ProcessStartMode.detached,
        );
      }
      if (fps && (s['fps_counter'] ?? 0) != 0) {
        await Process.start(await fpsExecutable(), [
          '--fps',
          '${s['PID']}',
          '${s['Created']}',
          '${s['fps_counter']}',
          p.join(game, clientExecutable),
        ], mode: ProcessStartMode.detached);
      }
      final account = await loadAccount(n);
      final savedAccounts = <Map<String, dynamic>>[];
      for (var number = 1; number <= 8; number++) {
        final saved = number == n ? account : await loadAccount(number);
        if ((saved['Account'] as String? ?? '').isEmpty) continue;
        if (!savedAccounts.any((old) => old['Account'] == saved['Account'] && old['Password'] == saved['Password'])) {
          savedAccounts.add(saved);
        }
      }
      for (var i = 0; i < 60; i++) {
        final filled = await native({
          ...s,
          'Op': 'window',
          'Action': 'fill',
          'Image': p.join(game, clientExecutable),
          ...account,
          'Accounts': savedAccounts,
        });
        if (filled == true) {
          status('窗口 $n 已填写账号密码，请在游戏内点击登录。');
          return;
        }
        await Future<void>.delayed(const Duration(milliseconds: 500));
      }
      status('游戏已启动，未找到登录输入框；若已登录，无需重复操作。');
    } finally {
      await lock.unlock();
      await lock.close();
    }
  }

  Future<String> logText() async {
    final f = File(p.join(shared, 'online-client.log'));
    if (!await f.exists()) return '暂无窗口日志';
    final handle = await f.open();
    try {
      final size = await handle.length();
      await handle.setPosition(size > 32768 ? size - 32768 : 0);
      return publicError(
        utf8.decode(await handle.read(32768), allowMalformed: true),
      );
    } finally {
      await handle.close();
    }
  }
}
