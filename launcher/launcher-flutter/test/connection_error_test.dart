import 'dart:async';
import 'dart:io';
import 'package:flutter_test/flutter_test.dart';
import 'package:openkfo_launcher/connection_error.dart';
import 'package:openkfo_launcher/launcher_service.dart';

class FailingHealth extends LauncherService {
  FailingHealth(this.failure) : super('.');
  final Exception failure;
  int calls = 0;
  @override
  Future<String> checkHealth() async {
    calls++;
    throw failure;
  }
}

void main() {
  test('real HTTP failures preserve status and only parse Cloudflare error pages', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final client = HttpClient();
    server.listen((request) async {
      request.response.statusCode = 530;
      if (request.uri.path == '/cf') request.response.headers.set('cf-ray', 'test');
      request.response.write('Error <span>1033</span> private.example');
      await request.response.close();
    });
    try {
      for (final path in ['/cf', '/other']) {
        final request = await client.getUrl(Uri.parse('http://127.0.0.1:${server.port}$path'));
        final failure = await serviceHttpFailure(await request.close(), '更新服务');
        expect(failure.status, 530);
        expect(failure.detailCode, path == '/cf' ? 1033 : null);
        expect(publicError(failure), contains('HTTP 530'));
        expect(publicError(failure), isNot(contains('private.example')));
      }
    } finally {
      client.close(force: true);
      await server.close(force: true);
    }
  });
  test('stalled error body stops reading without replacing the HTTP failure', () async {
    final body = StreamController<List<int>>();
    final watch = Stopwatch()..start();
    expect(await readCloudflareCode(body.stream), isNull);
    expect(watch.elapsed, lessThan(const Duration(seconds: 5)));
    await body.close();
  });
  test('health retries preserve typed causes and never retry TLS or HTTP failures', () async {
    for (final entry in <Exception, int>{
      TimeoutException('private.example'): 2,
      const SocketException('private.example', osError: OSError('private', 11001)): 2,
      const HandshakeException('private.example'): 1,
      ServiceHttpException(530, '服务器检查', detailCode: 1033): 1,
    }.entries) {
      final service = FailingHealth(entry.key);
      await expectLater(service.health(), throwsA(same(entry.key)));
      expect(service.calls, entry.value);
      final text = connectionFailureText(entry.key);
      expect(text, isNot(contains('private.example')));
      expect(text, isNot(contains('原因未确定')));
    }
  });
  test('HTTP errors remain distinct from local certificate failures', () async {
    final code = await readCloudflareCode(Stream.value('Error <span>1033</span> private.example'.codeUnits));
    expect(code, 1033);
    final error = ServiceHttpException(530, '更新服务', detailCode: code);
    expect(connectionFailureText(error), contains('隧道'));
    expect(error.toString(), isNot(contains('private.example')));
    expect(ServiceHttpException(526, '服务器检查').toString(), contains('源站证书'));
    expect(ServiceHttpException(530, '更新服务').toString(), contains('不能判断'));
    expect(await readCloudflareCode(Stream.value('error code: 1016'.codeUnits)), 1016);
    expect(await readCloudflareCode(Stream.value('${'x' * 8192} error code: 1033'.codeUnits)), isNull);
    expect(await readCloudflareCode(Stream.error(const SocketException('private'))), isNull);
  });
  test('connection failures explain the observed cause without blaming the user', () {
    expect(connectionFailureText(TimeoutException('private')), contains('超时'));
    expect(connectionFailureText(const HandshakeException('private')), contains('握手'));
    expect(connectionFailureText(StateError('No element')), contains('未返回'));
    expect(connectionFailureText(const SocketException('private', osError: OSError('private', 10061))), contains('连接被拒绝'));
    expect(connectionFailureText(const SocketException('private', osError: OSError('private', 11001))), contains('域名解析失败'));
    expect(connectionFailureText(Exception('private')), isNot(contains('private')));
  });
}
