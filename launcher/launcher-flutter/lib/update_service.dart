import 'dart:convert';
import 'dart:io';

import 'package:archive/archive.dart';
import 'package:path/path.dart' as p;

import 'launcher_service.dart';

bool safeRelative(String name) =>
    name.isNotEmpty &&
    !name.contains('..') &&
    !name.contains('\\') &&
    !name.contains(':') &&
    !name.startsWith('/') &&
    !name
        .split('/')
        .any((s) => s.isEmpty || s.endsWith('.') || s.endsWith(' '));
bool allowedLauncher(String name) =>
    safeRelative(name) &&
    (['启动器.exe', 'LauncherSupport.exe', 'native_assets.yaml'].contains(name) ||
        (!name.contains('/') && name.endsWith('.dll')) ||
        name.startsWith('data/') ||
        name.startsWith('launcher-files/'));
bool allowedClient(String name) =>
    safeRelative(name) &&
    ([
          'Data/',
          'effect/',
          'GPK/',
          'HostWidgets/',
          'SDO/',
          'spdata/',
          'UI/',
          'Weapon/',
        ].any(name.startsWith) ||
        (!name.contains('/') && name.toLowerCase().endsWith('.dll')));

class UpdateProgress {
  final String phase, file;
  final int index, count, received, size, overallReceived, overallSize;
  const UpdateProgress(
    this.phase,
    this.file,
    this.index,
    this.count,
    this.received,
    this.size,
    this.overallReceived,
    this.overallSize,
  );
  double? get fileFraction => size > 0 ? (received / size).clamp(0, 1) : null;
  double? get overallFraction =>
      overallSize > 0 ? (overallReceived / overallSize).clamp(0, 1) : null;
}

class UpdateService {
 String? verifiedLauncherVersion;
 String? verifiedClientVersion;
 String get verifiedRelease {
   if (verifiedLauncherVersion == null || verifiedLauncherVersion != verifiedClientVersion) {
     throw Exception('发布版本已变化或校验未完成，请重新检查更新后启动。');
   }
   return verifiedLauncherVersion!;
 }

  final LauncherService launcher;
  UpdateService(this.launcher, {this.onProgress});
  final void Function(UpdateProgress)? onProgress;
  bool get usesOss =>
      (launcher.config['update_version_url'] as String? ?? '').isNotEmpty;

  static Uri httpsUrl(String value) {
    final url = Uri.parse(value);
    if (url.scheme != 'https' ||
        url.host.isEmpty ||
        url.userInfo.isNotEmpty ||
        url.hasFragment) {
      throw const FormatException('更新地址必须是有效的 HTTPS 地址');
    }
    return url;
  }

  Future<String> announcement() async {
    final configured = launcher.config['announcement_url'] as String?;
    final url = configured != null && configured.isNotEmpty
        ? httpsUrl(configured)
        : httpsUrl(launcher.config['update_version_url'] as String).resolve('../announcement.json');
    final bytes = await download(url, 65536, missing: true);
    if (bytes == null) return '暂无公告';
    final value = jsonDecode(utf8.decode(bytes));
    if (value is! Map || value['content'] is! String ||
        (value['title'] != null && value['title'] is! String)) {
      throw const FormatException('公告格式无效');
    }
    final title = (value['title'] as String? ?? '').trim();
    final content = (value['content'] as String).trim();
    return [title, content].where((text) => text.isNotEmpty).join('\n');
  }

  Future<Map<String, dynamic>?> ossManifest({bool client = false}) async {
    final endpoint = httpsUrl(launcher.config['update_version_url'] as String);
    final version = jsonDecode(
      utf8.decode((await download(endpoint, 262144))!),
    );
    if (version is! Map ||
        version['version'] is! String ||
        (version['version'] as String).isEmpty) {
      throw const FormatException('版本文件无效');
    }
    final pointer = version[client ? 'client_manifest' : 'manifest'];
    if (client && pointer == null) return null;
    if (pointer is! String) throw const FormatException('版本文件缺少清单地址');
    final m = jsonDecode(
      utf8.decode((await download(httpsUrl(pointer), 1024 * 1024))!),
    );
    if (m is! Map<String, dynamic>) throw const FormatException('更新清单无效');
    final normalized = normalizeOss(
      m,
      version['version'] as String,
      client: client,
    );
    if (client) {
      validateClientFiles(normalized);
    } else {
      validate(normalized);
    }
    return normalized;
  }

  static Map<String, dynamic> normalizeOss(
    Map<String, dynamic> m,
    String version, {
    bool client = false,
  }) {
    if (m['version'] != version ||
        m['files'] is! List ||
        m['target'] != (client ? 'client' : 'launcher')) {
      throw const FormatException('版本和更新清单不一致');
    }
    final files = <String, dynamic>{};
    final seen = <String>{};
    for (final row in m['files'] as List) {
      if (row is! Map ||
          row['path'] is! String ||
          row['url'] is! String ||
          !seen.add((row['path'] as String).toLowerCase())) {
        throw const FormatException('更新文件地址无效或路径重复');
      }
      httpsUrl(row['url'] as String);
      files[row['path'] as String] = {
        'sha256': row['sha256'],
        'size': row['size'],
        'url': row['url'],
      };
    }
    return {
      ...m,
      'kind': client ? 'client-files' : 'launcher-flutter',
      'notes': m['notes'] ?? '',
      'files': files,
    };
  }

  static void validateClientFiles(Map<String, dynamic> m) {
    final files = m['files'];
    if (files is! Map ||
        files.isEmpty ||
        files.length > 16384 ||
        !RegExp(r'^[a-f0-9]{64}$')
            .hasMatch(m['config_hash'] is String ? m['config_hash'] : '')) {
      throw const FormatException('客户端文件清单无效');
    }
    var total = 0;
    final names = <String>{};
    for (final entry in files.entries) {
      final value = entry.value;
      if (entry.key is! String ||
          !allowedClient(entry.key) ||
          !names.add((entry.key as String).toLowerCase()) ||
          value is! Map ||
          value['size'] is! int ||
          value['size'] <= 0 ||
          value['size'] > 256 * 1024 * 1024 ||
          !RegExp(r'^[a-f0-9]{64}$')
              .hasMatch(value['sha256'] is String ? value['sha256'] : '') ||
          value['url'] is! String) {
        throw const FormatException('客户端文件校验信息无效');
      }
      httpsUrl(value['url']);
      total += value['size'] as int;
    }
    if (total > 512 * 1024 * 1024) throw const FormatException('客户端更新超过限制');
    if (files.containsKey('Data/config.spf2') &&
        files['Data/config.spf2']['sha256'] != m['config_hash']) {
      throw const FormatException('客户端配置指纹不一致');
    }
  }

  Uri fileUrl(dynamic entry) => entry['url'] is String
      ? httpsUrl(entry['url'])
      : uri('files/${entry['sha256']}.bin');

  Uri uri(String name) {
    final base = Uri.parse(launcher.config['update_base_url'] as String);
    if (base.scheme != 'https' || !base.path.endsWith('/')) {
      throw Exception('更新配置无效');
    }
    return base.resolve(name);
  }

  Future<List<int>?> download(
    Uri url,
    int max, {
    bool missing = false,
    void Function(int)? onBytes,
  }) async {
    if (url.scheme != 'https') throw Exception('更新地址必须使用 HTTPS');
    final client = HttpClient()
      ..connectionTimeout = const Duration(seconds: 15);
    try {
      final request = await client.getUrl(url);
      request.followRedirects = false;
      if (url.path.endsWith('.json')) {
        request.headers.set('Cache-Control', 'no-cache');
      }
      request.headers.set('User-Agent', 'OpenKFO-Launcher/Flutter');
      final response = await request.close().timeout(
        const Duration(seconds: 30),
      );
      if (missing && response.statusCode == 404) return null;
      if (response.statusCode != 200) {
        throw Exception('更新服务返回 ${response.statusCode}');
      }
      final bytes = <int>[];
      final refresh = Stopwatch()..start();
      onBytes?.call(0);
      await for (final chunk in response.timeout(const Duration(seconds: 30))) {
        bytes.addAll(chunk);
        if (bytes.length > max) throw Exception('下载文件过大');
        if (refresh.elapsedMilliseconds >= 100) {
          onBytes?.call(bytes.length);
          refresh.reset();
        }
      }
      onBytes?.call(bytes.length);
      return bytes;
    } finally {
      client.close(force: true);
    }
  }

  Future<Map<String, dynamic>?> check() async {
 verifiedLauncherVersion = null;
    if (launcher.local) return null;
    Map<String, dynamic> m;
    if (usesOss) {
      m = (await ossManifest())!;
    } else {
      final bytes = await download(
        uri('launcher-flutter.json'),
        262144,
        missing: true,
      );
      if (bytes == null) throw Exception('未找到启动器更新清单，无法确认是否为最新版。');
      m = jsonDecode(utf8.decode(bytes)) as Map<String, dynamic>;
    }
    validate(m);
    for (final entry in (m['files'] as Map<String, dynamic>).entries) {
      final file = File(p.join(launcher.root, entry.key));
      if (!await file.exists() ||
          await file.length() != entry.value['size'] ||
          await fileHash(file.path) != entry.value['sha256']) {
        return m;
      }
    }
    verifiedLauncherVersion = m['version'] as String;
    return null;
  }

  static void validate(Map<String, dynamic> m) {
    if (m['kind'] != 'launcher-flutter' ||
        m['version'] is! String ||
        m['notes'] is! String ||
        m['files'] is! Map) {
      throw Exception('更新清单无效');
    }
    final files = m['files'] as Map;
    var total = 0;
    if (files.length > 1024 ||
        ![
          '启动器.exe',
          'LauncherSupport.exe',
          'data/app.so',
          'flutter_windows.dll',
        ].every(files.containsKey)) {
      throw Exception('更新文件清单不完整');
    }
    final seen = <String>{};
    for (final e in files.entries) {
      if (e.key is! String ||
          !allowedLauncher(e.key) ||
          !seen.add((e.key as String).toLowerCase()) ||
          e.value is! Map ||
          e.value['size'] is! int ||
          e.value['size'] <= 0 ||
          e.value['size'] > 256 * 1024 * 1024 ||
          !RegExp(r'^[a-f0-9]{64}$').hasMatch(e.value['sha256'] ?? '')) {
        throw Exception('更新文件校验信息无效');
      }
      total += e.value['size'] as int;
    }
    if (total > 512 * 1024 * 1024) throw Exception('更新包超过限制');
  }

  Future<List<Map<String, dynamic>>> stageChanges(
    Map<String, dynamic> m,
    Directory stage,
    void Function(String) status,
  ) async {
    validate(m);
    final changed = <Map<String, dynamic>>[];
    final pending = <MapEntry<String, dynamic>>[];
    for (final entry in (m['files'] as Map<String, dynamic>).entries) {
      final existing = File(p.join(launcher.root, entry.key));
      if (await existing.exists() &&
          await existing.length() == entry.value['size'] &&
          await fileHash(existing.path) == entry.value['sha256']) {
        continue;
      }
      pending.add(entry);
    }
    final total = pending.fold<int>(
      0,
      (sum, e) => sum + (e.value['size'] as int),
    );
    var completed = 0;
    for (var i = 0; i < pending.length; i++) {
      final e = pending[i];
      final size = e.value['size'] as int;
      void progress(String phase, int received) => onProgress?.call(
        UpdateProgress(
          phase,
          e.key,
          i + 1,
          pending.length,
          received,
          size,
          completed + received,
          total,
        ),
      );
      status('正在下载 ${p.basename(e.key)}');
      progress('下载更新文件', 0);
      final bytes = (await download(
        fileUrl(e.value),
        size,
        onBytes: (n) => progress('下载更新文件', n),
      ))!;
      progress('校验更新文件', bytes.length);
      if (bytes.length != size || hashBytes(bytes) != e.value['sha256']) {
        throw Exception('下载校验失败，现有文件未替换');
      }
      await writeAtomic(p.join(stage.path, e.key), bytes);
      changed.add({'Name': e.key, 'SHA256': e.value['sha256'], 'Size': size});
      completed += size;
    }
    return changed;
  }

  Future<void> install(
    Map<String, dynamic> m,
    void Function(String) status,
  ) async {
    validate(m);
    final stage = await Directory.systemTemp.createTemp('OpenKFO-update-');
    final changed = await stageChanges(m, stage, status);
    if (changed.isEmpty) return;
    final exe = Platform.resolvedExecutable;
    final created = await launcher.native({
      'Op': 'info',
      'PID': pid,
      'Image': exe,
    });
    final helper = p.join(stage.path, 'UpdateHelper.exe');
    await File(launcher.support).copy(helper);
    final plan = p.join(stage.path, 'plan.json');
    await File(plan).writeAsString(
      jsonEncode({
        'Target': launcher.root,
        'Stage': stage.path,
        'Launcher': exe,
        'PID': pid,
        'Created': created,
        'Files': changed,
      }),
    );
    await writeAtomic(
      p.join(launcher.root, '.flutter-update-pending'),
      utf8.encode(plan),
    );
    await Process.start(
      helper,
      ['--apply', plan],
      mode: ProcessStartMode.detached,
      workingDirectory: stage.path,
    );
    exit(0);
  }

  Future<Map<String, dynamic>?> clientCheck() async {
 verifiedClientVersion = null;
    if (launcher.local) return null;
    if (usesOss) {
      final manifest = await ossManifest(client: true);
      if (manifest == null) return null;
      for (final entry in (manifest['files'] as Map<String, dynamic>).entries) {
        final file = File(p.join(launcher.game, entry.key));
        if (!await file.exists() ||
            await file.length() != entry.value['size'] ||
            await fileHash(file.path) != entry.value['sha256']) {
          return manifest;
        }
      }
      if (await fileHash(p.join(launcher.game, 'Data/config.spf2')) !=
          manifest['config_hash']) {
        throw Exception('客户端配置不匹配，请发布完整资源清单');
      }
      verifiedClientVersion = manifest['version'] as String;
      return null;
    }
    var raw = await download(uri('client.json'), 65536, missing: true);
    raw ??= await download(uri('weapons.json'), 65536, missing: true);
    if (raw == null) return null;
    final m = jsonDecode(utf8.decode(raw)) as Map<String, dynamic>;
    if (!['client', 'weapons'].contains(m['kind']) ||
        !RegExp(r'^[a-f0-9]{64}$').hasMatch(m['sha256'] ?? '') ||
        m['package'] != '${m['sha256']}.zip' ||
        m['size'] is! int ||
        m['size'] <= 0 ||
        m['size'] > 256 * 1024 * 1024 ||
        !RegExp(r'^[a-f0-9]{64}$').hasMatch(m['config_hash'] ?? '')) {
      throw Exception('客户端更新清单无效');
    }
    final receipt = File(
      p.join(launcher.game, 'flutter-client-installed.json'),
    );
    if (await receipt.exists()) {
      final installed = jsonDecode(await receipt.readAsString());
      if (installed['sha256'] == m['sha256']) {
        var valid = true;
        for (final e in (installed['files'] as Map<String, dynamic>).entries) {
          if (!allowedClient(e.key) ||
              !await File(p.join(launcher.game, e.key)).exists() ||
              await fileHash(p.join(launcher.game, e.key)) != e.value) {
            valid = false;
            break;
          }
        }
        if (valid) return null;
      }
    }
    // Existing configurations may already match a legacy weapons-only release.
    if (m['kind'] == 'weapons' &&
        await fileHash(p.join(launcher.game, 'Data', 'config.spf2')) ==
            m['config_hash']) {
      return null;
    }
    return m;
  }

  Future<void> installClient(
    Map<String, dynamic> m,
    void Function(String) status,
  ) async {
    for (var n = 1; n <= 8; n++) {
      if (await launcher.state(n) != null) {
        throw Exception('更新客户端前，请先关闭全部游戏窗口。');
      }
    }
    final files = <String, List<int>>{};
    var total = 0;
    if (m['kind'] == 'client-files') {
      validateClientFiles(m);
      final entries = (m['files'] as Map<String, dynamic>).entries.toList();
      final pending = <MapEntry<String, dynamic>>[];
      for (final entry in entries) {
        final file = File(p.join(launcher.game, entry.key));
        if (!await file.exists() ||
            await file.length() != entry.value['size'] ||
            await fileHash(file.path) != entry.value['sha256']) {
          pending.add(entry);
        }
      }
      final bytesTotal = pending.fold<int>(
        0,
        (sum, e) => sum + (e.value['size'] as int),
      );
      var received = 0;
      for (var i = 0; i < pending.length; i++) {
        final entry = pending[i];
        final size = entry.value['size'] as int;
        status('正在下载 ${entry.key}');
        final data = (await download(
          fileUrl(entry.value),
          size,
          onBytes: (n) => onProgress?.call(
            UpdateProgress(
              '下载客户端文件',
              entry.key,
              i + 1,
              pending.length,
              n,
              size,
              received + n,
              bytesTotal,
            ),
          ),
        ))!;
        if (data.length != size || hashBytes(data) != entry.value['sha256']) {
          throw Exception('下载校验失败，现有文件未替换');
        }
        files[entry.key] = data;
        total += size;
        received += size;
      }
    } else {
      status('正在下载客户端更新…');
      final packageSize = m['size'] as int;
      final label = m['kind'] == 'weapons' ? '武器配置更新包' : '客户端资源更新包';
      onProgress?.call(
        UpdateProgress('下载更新包', label, 1, 1, 0, packageSize, 0, packageSize),
      );
      final bytes = (await download(
        uri(m['package']),
        packageSize,
        onBytes: (n) => onProgress?.call(
          UpdateProgress('下载更新包', label, 1, 1, n, packageSize, n, packageSize),
        ),
      ))!;
      onProgress?.call(
        UpdateProgress(
          '校验更新包',
          label,
          1,
          1,
          bytes.length,
          packageSize,
          bytes.length,
          packageSize,
        ),
      );
      await Future<void>.delayed(Duration.zero);
      if (bytes.length != m['size'] || hashBytes(bytes) != m['sha256']) {
        throw Exception('客户端更新校验失败');
      }
      final archive = ZipDecoder().decodeBytes(bytes);
      final entries = archive.where((entry) => entry.isFile).toList();
      var checked = 0;
      for (final entry in entries) {
        if (!entry.isFile) continue;
        if (!allowedClient(entry.name) ||
            entry.isSymbolicLink ||
            files.keys.any(
              (k) => k.toLowerCase() == entry.name.toLowerCase(),
            )) {
          throw Exception('客户端更新路径无效');
        }
        total += entry.size;
        if (total > 512 * 1024 * 1024) throw Exception('客户端解压超过限制');
        files[entry.name] = entry.content;
        checked++;
        onProgress?.call(
          UpdateProgress(
            '解压并校验文件',
            entry.name,
            checked,
            entries.length,
            entry.size,
            entry.size,
            checked,
            entries.length,
          ),
        );
        await Future<void>.delayed(Duration.zero);
      }
    }
    final configDigest = files.containsKey('Data/config.spf2')
        ? hashBytes(files['Data/config.spf2']!)
        : await fileHash(p.join(launcher.game, 'Data/config.spf2'));
    if (configDigest != m['config_hash']) {
      throw Exception('客户端配置校验失败');
    }
    final stage = await Directory.systemTemp.createTemp('OpenKFO-client-');
    final backups = <String, List<int>?>{};
    try {
      for (final e in files.entries) {
        final dest = p.join(launcher.game, e.key);
        final f = File(dest);
        backups[dest] = await f.exists() ? await f.readAsBytes() : null;
      }
      // Keep a disk recovery record before replacement as well as immediate rollback.
      for (final e in backups.entries) {
        if (e.value != null) {
          await writeAtomic(
            p.join(stage.path, p.relative(e.key, from: launcher.game)),
            e.value!,
          );
        }
      }
      await File(p.join(stage.path, 'recovery.json')).writeAsString(
        jsonEncode({'target': launcher.game, 'files': backups.keys.toList()}),
      );
      var installed = 0, installedBytes = 0;
      for (final e in files.entries) {
        onProgress?.call(
          UpdateProgress(
            '安装文件',
            e.key,
            installed + 1,
            files.length,
            0,
            e.value.length,
            installedBytes,
            total,
          ),
        );
        await writeAtomic(p.join(launcher.game, e.key), e.value);
        installed++;
        installedBytes += e.value.length;
        onProgress?.call(
          UpdateProgress(
            '安装文件',
            e.key,
            installed,
            files.length,
            e.value.length,
            e.value.length,
            installedBytes,
            total,
          ),
        );
      }
      launcher.config['config_hash'] = m['config_hash'];
      await writeAtomic(
        launcher.configPath,
        utf8.encode(jsonEncode(launcher.config)),
      );
      await writeAtomic(
        p.join(launcher.game, 'flutter-client-installed.json'),
        utf8.encode(
          jsonEncode({
            ...m,
            'files': files.map((k, v) => MapEntry(k, hashBytes(v))),
          }),
        ),
      );
      onProgress?.call(
        UpdateProgress(
          '更新完成',
          '客户端资源',
          files.length,
          files.length,
          total,
          total,
          total,
          total,
        ),
      );
    } catch (_) {
      onProgress?.call(
        UpdateProgress('更新失败，正在恢复原文件', '', 0, files.length, 0, 0, 0, 0),
      );
      for (final e in backups.entries) {
        if (e.value != null) {
          await writeAtomic(e.key, e.value!);
        } else {
          final f = File(e.key);
          if (await f.exists()) await f.delete();
        }
      }
      rethrow;
    }
  }
}
