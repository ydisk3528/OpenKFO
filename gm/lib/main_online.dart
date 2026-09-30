import 'gm_version.dart';

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math' as math;

import 'package:flutter/material.dart';

import 'main.dart' show Manager;

// No SSH key, database password, or administrator token is bundled in the app.
class OnlineBackend {
  OnlineBackend(String address, this.token)
    : endpoint = Uri.parse(address.trim()) {
    if (endpoint.scheme != 'https' ||
        endpoint.host.isEmpty ||
        endpoint.userInfo.isNotEmpty ||
        endpoint.hasQuery ||
        endpoint.hasFragment ||
        endpoint.path != '/gm/api') {
      throw const FormatException('请输入 https://管理域名/gm/api');
    }
    if (token.length < 32) throw const FormatException('管理令牌至少需要 32 个字符');
  }
  final Uri endpoint;
  final String token;
  Future<dynamic> call(Map<String, dynamic> input) async {
    if (input['environment'] == 'local') throw StateError('此版本仅允许线上管理');
    final client = HttpClient()
      ..connectionTimeout = const Duration(seconds: 10);
    try {
      return await (() async {
        final request = await client.postUrl(endpoint);
        request.followRedirects = false;
        request.headers.contentType = ContentType.json;
        request.headers.set(HttpHeaders.authorizationHeader, 'Bearer $token');
        request.write(
          jsonEncode({
            ...Map<String, dynamic>.from(input)..remove('_refresh'),
            'environment': 'online',
            'gm_version': gmVersion,
          }),
        );
        final response = await request.close();
        if (response.statusCode == 401) throw StateError('管理令牌无效');
        if (response.isRedirect) throw StateError('管理地址不能重定向，请填写最终 HTTPS 地址');
        final bytes = <int>[];
        await for (final chunk in response) {
          if (bytes.length + chunk.length > 16 * 1024 * 1024) {
            throw StateError('响应过大');
          }
          bytes.addAll(chunk);
        }
        final data = jsonDecode(utf8.decode(bytes));
        if (response.statusCode != 200 || data['ok'] != true) {
          throw StateError(data['error']?.toString() ?? '管理请求失败');
        }
        return data['result'];
      })().timeout(const Duration(seconds: 85));
    } on TimeoutException {
      throw StateError('请求超时，写入结果可能尚未收到；请保持原操作重试');
    } finally {
      client.close(force: true);
    }
  }
}

void main() => runApp(const OnlineManagerApp());

class OnlineManagerApp extends StatelessWidget {
  const OnlineManagerApp({super.key});
  @override
  Widget build(BuildContext context) => MaterialApp(
    title: '功夫小子 · GM线上管理',
    debugShowCheckedModeBanner: false,
    theme: ThemeData(
      useMaterial3: true,
      colorSchemeSeed: const Color(0xFF087E83),
    ),
    // Existing management tables retain usable cell widths; compact screens can pan.
    builder: (context, child) => LayoutBuilder(
      builder: (context, size) => Scrollbar(
        child: SingleChildScrollView(
          scrollDirection: Axis.horizontal,
          child: SizedBox(
            width: math.max(size.maxWidth, 1180),
            child: SingleChildScrollView(
              child: SizedBox(
                height: math.max(size.maxHeight, 760),
                child: child,
              ),
            ),
          ),
        ),
      ),
    ),
    home: const OnlineConnection(),
  );
}

class OnlineConnection extends StatefulWidget {
  const OnlineConnection({super.key});
  @override
  State<OnlineConnection> createState() => _OnlineConnectionState();
}

class _OnlineConnectionState extends State<OnlineConnection> {
  final address = TextEditingController(), token = TextEditingController();
  bool busy = false;
  String error = '';
  @override
  void dispose() {
    address.dispose();
    token.dispose();
    super.dispose();
  }

  Future<void> connect() async {
    setState(() {
      busy = true;
      error = '';
    });
    try {
      final backend = OnlineBackend(address.text, token.text.trim());
      await backend.call({'operation': 'accounts'});
      if (!mounted) return;
      token.clear();
      await Navigator.of(context).push(
        MaterialPageRoute<void>(
          builder: (_) => Manager(api: backend.call, onlineOnly: true),
        ),
      );
    } catch (_) {
      if (mounted) setState(() => error = '连接失败，请检查 HTTPS 地址、证书、管理令牌及服务器配置。');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('GM 线上管理')),
    body: Align(
      alignment: Alignment.topLeft,
      child: SizedBox(
        width: 360,
        child: Padding(
          padding: const EdgeInsets.all(20),
          child: Column(
            children: [
              const Text('连接自己的线上管理接口。令牌仅保存在当前会话，不写入安装包或本地文件。'),
              TextField(
                controller: address,
                decoration: const InputDecoration(
                  labelText: 'https://管理域名/gm/api',
                ),
                keyboardType: TextInputType.url,
              ),
              TextField(
                controller: token,
                obscureText: true,
                enableSuggestions: false,
                autocorrect: false,
                decoration: const InputDecoration(labelText: '管理令牌'),
              ),
              const SizedBox(height: 16),
              FilledButton(
                onPressed: busy ? null : connect,
                child: Text(busy ? '连接中…' : '连接线上服务器'),
              ),
              Text(error),
              const Text('手机上可横向滑动查看完整表格，建议横屏使用。'),
            ],
          ),
        ),
      ),
    ),
  );
}
