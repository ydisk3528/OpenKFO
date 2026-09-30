# OSS 发布工具 1.1.0（二区一键同步）

供发布者在 Windows 10/11 上使用，不分发给玩家。启动器仍通过 HTTPS 下载更新。

## 使用

1. 打开 `OSS发布工具-二区一键同步.exe`。Bucket 固定 `openkfo`、地域固定 `cn-hangzhou`、下载根地址固定 `https://openkfo.oss-cn-hangzhou.aliyuncs.com/`，三个字段不可编辑。
2. 选择由 `Prepare-OssRelease.py` 生成的 **OSS 上传包 ZIP**，例如 `OSS上传包-2026.09.23-oss.4.zip`。普通启动器 ZIP 不包含版本清单，不能直接发布。
3. 点击“仅检查压缩包”：不需要密钥、不连接 OSS；显示版本和更新说明。
4. 填写有目标 Bucket `oss:GetObject` 和 `oss:PutObject` 权限的 RAM AccessKey ID / Secret；使用临时凭据时填写 STS Token。
5. 选择二区 SSH 私钥。本机默认原有的 `kk.pem` 路径，私钥不会打入 EXE、更新包或上传 OSS。同步固定连接二区 `47.122.124.138`，不使用旧的一区管理配置。电脑需有 OpenSSH 客户端，且事先核验并信任二区主机密钥；工具不自动跳过主机校验。
6. 可先点击“预览合并清单”。点击“一键发布并同步二区”并确认维护提示后，工具合并历史清单、上传并回读校验资源，然后在发布版本入口之前预检二区与新配置兼容性。
7. 预检会检查 `bridge.json` 的配置哈希、二区数据库身份、当前关卡绑定、旧配置快照，以及地图目录/关卡脚本/怪物模板兼容性。地图规则变化会阻止发布，需先另行完成服务器配置迁移，不能强行只改哈希。
8. 预检通过才发布 `version/version.json`，随后同步二区。配置哈希相同时不重启；变化时先备份、停止 `kungfu-go`、迁移关卡绑定及玩家解锁记录、更新服务器 `config.json` 中的哈希、启动并检查健康状态。商城价格、背包、点券、游戏服务二进制都不被覆盖。
9. 必须看到“OSS 版本入口已确认，二区哈希同步成功”。如果 OSS 成功但二区失败，使用“重试同步当前版本”，不要随意创建新版本号或回滚别人的发布。进程异常中断或数据库结果不确定时可能保持服务停止并生成恢复记录，需管理员处理。
10. 最后用旧启动器实际检查更新并登录、建房测试。工具校验成功不能代替玩家端下载安装和游戏测试。

独立公告：进入“独立公告”页，读取或填写标题、正文，再点击“上传公告”。只更新根目录 `announcement.json`，不更改软件版本。更新 ZIP 内即使带有公告，也不会自动覆盖当前公告。

## 配置和错误处理

- `settings.json` 仅保存二区私钥路径，旧文件中的 Bucket、地域、下载地址不再生效。
- 密钥以当前 Windows 用户的 DPAPI 加密保存到 `%LOCALAPPDATA%\OpenKFO\Publisher\credentials.dpapi`，下次自动填入，也支持环境变量。换电脑或用户需要重新填写；EXE、源码、ZIP 均不含密钥或 SSH 私钥。
- 下载根地址必须与 ZIP 清单里的 HTTPS 地址一致。此工具不会偷偷改写下载地址或重做清单哈希。
- 上传前备份旧版本入口到 `%LOCALAPPDATA%\OpenKFO\Publisher\backups`。
- 相同版本资源已存在且 SHA-256 一致时跳过；内容不同则停止，必须重新构建新版本号。
- 网络错误可重新选择同一 ZIP 重试，已上传且校验一致的资源会复用。
- 取消会在当前请求结束后生效；进入最后版本发布阶段后不可取消。中途取消可能留下未被版本入口引用的资源，不会删除远端文件。
- 同一个 Bucket 请一次只运行一个发布任务；工具会在发布前检查版本入口是否被其他人改变，但不提供分布式发布锁。
- 每次只需打包本次修改。工具自动继承当前线上客户端清单，同路径以本次为准；历史对象继续引用原地址，无需重新上传。不要删除仍被最新清单引用的 OSS 历史对象。此机制无法找回在旧清单中已经丢失的文件，也不会合并 config.spf2 内部条目，该文件必须是合并好的完整配置。
- 远端回读校验会产生 OSS 下载流量。历史引用缺失或哈希不符会停止发布；玩家仍按自己的文件差异下载。预览只核对清单合并，实际资源完整性在正式发布时验证。
- 只读本地检查无需 OSS 凭据。普通更新 ZIP 仍由 `Prepare-OssRelease.py` 生成；本工具不合并 `config.spf2` 内部武器配置，也不会自动上架商城。

## 二区同步程序

`server/go-server/cmd/oss-sync` 构建为 Linux amd64 的 `/opt/kungfu-go/oss-sync-helper`，权限 0700。只通过 SSH stdin 接收 JSON，不监听公网端口，不需要 OSS 写入密钥。数据库凭据从运行中的二区服务环境获取，并验证数据库名为 `kungfu_realm2`。首次相同哈希同步会从 OSS 校验并保存旧配置快照到 `/opt/kungfu-go/oss-sync/config.spf2`。

变更备份保存在 `/opt/kungfu-go/oss-sync/backup-*`。若存在 `recovery.json`，应先核对服务器配置、数据库关卡绑定和运行状态；不要直接删除恢复记录或锁文件再强行发布。工具不会擅自覆盖未知的数据库提交结果。

此次实测：当前线上版本的同哈希预检/同步通过，服务 PID 未变。新哈希停止、迁移、启动分支尚未在生产触发；不得宣称已经实测过生产重启。

## 开发与测试

Python 3.14 / Tkinter；SDK `alibabacloud-oss-v2==1.4.0`。工具操作端为 Windows 10/11，与玩家端 Win7 兼容需求无关。

```powershell
python -m pip install -r tools/oss-publisher/requirements.txt
python tools/oss-publisher/app.py
# 在 tools/oss-publisher 目录：
python -m unittest -v test_publisher test_realm_sync
# 仓库根目录构建独立 EXE：
powershell -File tools/Build-OssPublisher.ps1 -Python C:/Python314/python.exe
```

官方 SDK：https://github.com/aliyun/alibabacloud-oss-python-sdk-v2
