import 'dart:async';
import 'dart:io';
import 'package:flutter_test/flutter_test.dart';
import 'package:openkfo_launcher/connection_error.dart';

void main() {
  test('connection failures explain the observed cause without blaming the user', () {
    expect(connectionFailureText(TimeoutException('private')), contains('超时'));
    expect(connectionFailureText(const HandshakeException('private')), contains('握手'));
    expect(connectionFailureText(StateError('No element')), contains('未返回'));
    expect(connectionFailureText(const SocketException('private', osError: OSError('private', 10061))), contains('连接被拒绝'));
    expect(connectionFailureText(const SocketException('private', osError: OSError('private', 11001))), contains('域名解析失败'));
    expect(connectionFailureText(Exception('private')), isNot(contains('private')));
  });
}
