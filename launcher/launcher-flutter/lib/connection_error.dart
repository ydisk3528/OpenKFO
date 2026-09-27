import 'dart:async';
import 'dart:io';

String connectionFailureText(Object error) {
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
