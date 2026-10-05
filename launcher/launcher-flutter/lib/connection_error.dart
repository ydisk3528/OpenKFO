import 'dart:async';
import 'dart:convert';
import 'dart:io';

class ServiceHttpException implements Exception {
  ServiceHttpException(this.status, this.service, {this.detailCode});
  final int status;
  final String service;
  final int? detailCode;

  @override
  String toString() {
    final code = detailCode == null ? '' : '，子错误 $detailCode';
    final reason = switch (status) {
      530 when detailCode == 1033 => '隧道暂不可用，请管理员检查隧道连接和服务状态。',
      530 => '网关解析或路由异常，请管理员检查 DNS 和隧道配置；此状态码不能判断为本机缺少证书。',
      525 => '网关与源站 TLS 握手失败，请管理员检查源站 TLS 配置。',
      526 => '网关无法验证源站证书，请管理员检查源站证书。',
      403 => '访问被拒绝，请管理员检查访问规则。',
      >= 500 => '服务或网关暂不可用，请稍后重试并联系管理员。',
      _ => '请联系管理员检查服务配置。',
    };
    return '$service失败（HTTP $status$code）。$reason';
  }
}

Future<ServiceHttpException> serviceHttpFailure(
    HttpClientResponse response, String service) async {
  int? code;
  final cloudflare = response.headers.value('cf-ray') != null ||
      (response.headers.value('server') ?? '').toLowerCase().contains('cloudflare');
  if (response.statusCode == 530 && cloudflare) {
    code = await readCloudflareCode(response);
  }
  return ServiceHttpException(response.statusCode, service, detailCode: code);
}

// Keep only a numeric diagnostic code, never the HTML, domain or IP address.
Future<int?> readCloudflareCode(Stream<List<int>> body) async {
  final iterator = StreamIterator(body);
  final bytes = <int>[];
  final watch = Stopwatch()..start();
  try {
    while (bytes.length < 8192) {
      final remaining = const Duration(seconds: 2) - watch.elapsed;
      if (remaining <= Duration.zero || !await iterator.moveNext().timeout(remaining)) break;
      bytes.addAll(iterator.current.take(8192 - bytes.length));
      final text = utf8.decode(bytes, allowMalformed: true).replaceAll(RegExp(r'<[^>]*>'), ' ');
      final match = RegExp(r'\berror\s*(?:code\s*)?[:\s]+(1\d{3})\b', caseSensitive: false).firstMatch(text);
      if (match != null) return int.tryParse(match.group(1)!);
    }
  } catch (_) {
    // A broken error page must not hide the original HTTP status.
  } finally {
    await iterator.cancel();
  }
  return null;
}

String connectionFailureText(Object error) {
  if (error is ServiceHttpException) return error.toString();
  if (error is TimeoutException) {
    return '服务器连接或回复超时，请稍后重试';
  }
  if (error is HandshakeException || error is TlsException) {
    return '安全连接失败：证书校验或握手未完成，请核对电脑时间、配套证书；也可能是服务端关闭了连接';
  }
  if (error is FileSystemException) {
    return '无法读取连接所需文件，请检查启动器配套证书是否完整';
  }
  if (error is SocketException) {
    final code = error.osError?.errorCode;
    if (code == 11001 || code == 11004 || code == -2) {
      return '服务器域名解析失败，请检查 DNS 或服务器地址';
    }
    if (code == 10061 || code == 111) {
      return '连接被拒绝：服务器端口未接受连接，请联系管理员';
    }
    if (code == 10060 || code == 110) {
      return '服务器连接超时，请稍后重试';
    }
    return '连接中断${code == null ? '' : '（错误码 $code）'}，可能是服务器或线路异常，请重试';
  }
  if (error is StateError) {
    return '服务器关闭连接，未返回检查结果；可能是服务端限制或连接中断';
  }
  return '服务器检查未完成，原因未确定，请查看或导出日志';
}
