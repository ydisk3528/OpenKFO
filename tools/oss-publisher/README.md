# OSS 发布工具

供发布者在 Windows 10/11 上使用，不分发给玩家。启动器仍通过 HTTPS 下载更新。

## 使用

1. 打开 `OSS发布工具.exe`。默认 Bucket 为 `openkfo`，地域 `cn-hangzhou`。
2. 选择由 `Prepare-OssRelease.py` 生成的 **OSS 上传包 ZIP**，例如 `OSS上传包-2026.09.23-oss.4.zip`。普通启动器 ZIP 不包含版本清单，不能直接发布。
3. 点击“仅检查压缩包”：不需要密钥、不连接 OSS；显示版本和更新说明。
4. 填写有目标 Bucket `oss:GetObject` 和 `oss:PutObject` 权限的 RAM AccessKey ID / Secret；使用临时凭据时填写 STS Token。
5. 可先点击“预览合并清单”：只读取线上清单，不上传，显示继承、新增、替换的文件。点击“解压并上传发布”时会重新合并最新线上清单，校验 SHA-256、上传资源、回读校验，最后更新 `version/version.json`。
6. 完成后在玩家旧启动器中点击“检查更新”。工具的成功表示 OSS 对象已回读确认，不能代替玩家端下载、安装和游戏登录测试。

独立公告：进入“独立公告”页，读取或填写标题、正文，再点击“上传公告”。只更新根目录 `announcement.json`，不更改软件版本。更新 ZIP 内即使带有公告，也不会自动覆盖当前公告。

## 配置和错误处理

- 仅 Bucket、地域、下载根地址保存到 `%LOCALAPPDATA%\OpenKFO\Publisher\settings.json`。
- 密钥只保留在当前进程内存，也可通过 `OSS_ACCESS_KEY_ID`、`OSS_ACCESS_KEY_SECRET`、`OSS_SESSION_TOKEN` 环境变量提供。不要发给玩家，不要加入 Git。
- 下载根地址必须与 ZIP 清单里的 HTTPS 地址一致。此工具不会偷偷改写下载地址或重做清单哈希。
- 上传前备份旧版本入口到 `%LOCALAPPDATA%\OpenKFO\Publisher\backups`。
- 相同版本资源已存在且 SHA-256 一致时跳过；内容不同则停止，必须重新构建新版本号。
- 网络错误可重新选择同一 ZIP 重试，已上传且校验一致的资源会复用。
- 取消会在当前请求结束后生效；进入最后版本发布阶段后不可取消。中途取消可能留下未被版本入口引用的资源，不会删除远端文件。
- 同一个 Bucket 请一次只运行一个发布任务；工具会在发布前检查版本入口是否被其他人改变，但不提供分布式发布锁。
- 每次只需打包本次修改。工具自动继承当前线上客户端清单，同路径以本次为准；历史对象继续引用原地址，无需重新上传。不要删除仍被最新清单引用的 OSS 历史对象。此机制无法找回在旧清单中已经丢失的文件，也不会合并 config.spf2 内部条目，该文件必须是合并好的完整配置。
- 远端回读校验会产生 OSS 下载流量。历史引用缺失或哈希不符会停止发布；玩家仍按自己的文件差异下载。预览只核对清单合并，实际资源完整性在正式发布时验证。
- 只读检查和本地测试无需 OSS 凭据。本次开发未上传任何线上对象。

## 开发与测试

Python 3.14 / Tkinter；SDK `alibabacloud-oss-v2==1.4.0`。工具操作端为 Windows 10/11，与玩家端 Win7 兼容需求无关。

```powershell
python -m pip install -r tools/oss-publisher/requirements.txt
python tools/oss-publisher/app.py
# 在 tools/oss-publisher 目录：
python -m unittest -v test_publisher
# 仓库根目录构建独立 EXE：
powershell -File tools/Build-OssPublisher.ps1 -Python C:/Python314/python.exe
```

官方 SDK：https://github.com/aliyun/alibabacloud-oss-python-sdk-v2
