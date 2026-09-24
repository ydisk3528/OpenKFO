# OpenKFO / KFO Server Emulator

面向学习与技术研究格斗游戏服务器模拟器项目，研究网络协议、服务端架构、数据持久化及客户端互操作，包含服务端、客户端配套工具和GM管理器源码。

> **非商业 · 学习研究 · 非官方项目**
>
> 本项目不以营利为目的，不收费、不投放广告、不接受捐赠或赞助，不提供充值、付费会员、付费道具或其他变相收费服务。请勿将本项目用于商业运营或未经授权的游戏服务。

## 项目来源与致谢

OpenKFO 基于 [liuyangyi0/kungfukid-local-server](https://github.com/liuyangyi0/kungfukid-local-server) 修改和扩展，感谢原项目作者开源并提供基础实现。

特别感谢 **QQ：512222607、348159579** 251919129。

本仓库在原项目基础上进行了目录整理、Go 服务端扩展、登录器与GM管理器集成，以及构建打包流程调整。原项目已有实现的贡献归原作者，OpenKFO 的后续修改不代表原作者的观点或背书。

## 项目定位与使用边界

- **学习与研究**：面向个人学习、协议兼容性研究和获得必要授权的测试，不以替代官方服务或招揽玩家运营为目标。道具、商城与钱包管理功能用于开发测试，不用于真实货币交易。
- **独立实现原则**：服务端以独立实现为原则；不引入泄露的官方服务端源码、商业秘密或来源不明的代码。第三方依赖应保留其许可和署名，贡献代码需说明来源并具有相应提交权限。
- **不分发官方内容**：本仓库不提供原游戏客户端、官方美术、音频、地图等资源的下载或镜像。请勿在提交、Issue、附件或 Release 中上传未经授权的程序和素材。
- **客户端自行合法取得**：研究者需自行通过合法渠道取得客户端，并确认相应使用权限；项目仅在必要权限范围内提供兼容代码、连接配置与补丁使用说明，不授予原客户端或游戏素材的使用、复制或传播权利。
- **非官方、无隶属关系**：OpenKFO 并非官方产品，与原游戏的开发商、发行商及运营方无隶属、合作或背书关系。《功夫小子》及相关名称、商标和作品的权利归各自权利人所有，文中提及仅用于说明兼容研究对象。
- **尊重权利与技术措施**：请勿利用本项目未经许可复制、传播第三方软件或素材，或违法规避、破坏著作权保护技术措施。开展研究及公开测试前，应确认必要授权与适用的使用条件。

**“免费”“非商业”或“仅供学习”并不当然意味着合法，也不构成免责或权利人的授权。** 本声明说明项目定位和维护原则，不代替源代码许可证或第三方许可，也不构成对任何具体使用方式合法性的保证。

如权利人认为仓库中的具体内容涉及其权利，请通过 [GitHub Issue](https://github.com/ydisk3528/OpenKFO/issues) 提供文件路径、权利依据及问题说明；维护者将核查并根据情况处理。请勿公开提交身份证件、密钥或其他敏感材料。

法律参考：[2025 年“两高”《关于办理侵犯知识产权刑事案件适用法律若干问题的解释》](https://www.court.gov.cn/zixun/xiangqing/463291.html)。本项目不将其中的入罪数额标准视为允许侵权的范围。

---

## 先看这一页：按什么顺序做

**准备客户端与数据库 → 启动 Go 服务器 → 构建并配置登录器 → 验证游戏登录 → 构建并连接 GM 管理器。**

先把本地连接跑通，再改线上地址。日志窗口、协议测试 APP、自动更新都是后续可选项，不要一开始混在一起部署。当前运行不依赖 Python，也不要求使用 PowerShell。

给接手项目的 AI：先确认用户已有的客户端目录、数据库和配置，保留现有数据；不要凭空填写地图、角色选项或哈希，不要把历史 Python 服务当成当前入口。缺少配置就明确指出缺哪项，不能把“编译成功”说成“可以登录”。

## 第 1 步：找到源码，安装工具

| 要做什么 | 当前源码入口 | 需要什么 |
| --- | --- | --- |
| 游戏服务器、数据库、协议逻辑 | `server/go-server/cmd/server`、`internal/game`、`internal/persistence` | Go 1.26.0、MySQL 8；Windows/Linux/macOS |
| 本地和线上登录器（同一份源码） | `launcher/launcher-online` | Windows、.NET 8 SDK、Go |
| 游戏内登录界面组件 | `launcher/client-adapter` | Windows、VS 2022 x86 C++ 工具链 |
| GM 完整版 | `gm/lib/main.dart` + `server/go-server/cmd/desktop-admin` | Windows、Flutter（Dart ^3.13.2）、VS C++、Go |
| GM macOS/Android 线上版 | `gm/lib/main_online.dart` | Flutter、目标平台工具链、已部署 GM HTTPS API |
| 本地服务器日志窗口 | `tools/local-server-monitor` | Windows、.NET 8 SDK |
| 协议测试 APP | `launcher/protocol-tester` + `server/go-server/cmd/protocol-tester` | Windows、.NET 8 SDK、Go |
| 独立更新助手 | `tools/updater` | Windows、.NET 8 SDK |

`protocol/` 是协议及实现进度，`docs/` 是补充资料，`dist/` 是构建输出。**`launcher/launcher`、`server/kk_local`、`server/tests` 和 Python 脚本是历史实现，不用于下面的启动流程。**

以下每个命令块都从**仓库根目录**开始。推荐源码放在英文路径，例如 `C:/src/OpenKFO`，游戏资源另放，避免 Flutter/MSBuild 的路径兼容问题。

## 数据库和配置填写入口

首次部署先读 [数据库结构与初始化](docs/DatabaseSchema.md)，再按 [配置填写手册](docs/Configuration.md) 填文件。手册包含本机MySQL、SSH独立测试库、登录器和GM的完整配置示例，明确文件放哪里、字段填什么、相对路径按哪里解析。

- 数据库：51张表的用途、全部字段/主键/外键SQL、创建数据库与初始化顺序。
- 配置：`settings.private.json`、`ssh.private.json`、`config.json`、`bridge.json`/`bridge.local.json`、`gm-settings.json`、`online-admin.json`和`updater-settings.json`。
- 首次空库先导入 [database-schema.sql](docs/database-schema.sql)，再启动Go服务；已有库不要删库重建。

## 第 2 步：先准备服务器配置

1. 自行合法取得兼容客户端，确认 `gfld.dat` 和 `Data/config.spf2` 存在。游戏资源不放进 Git。
2. 按 [数据库初始化步骤](docs/DatabaseSchema.md) 创建数据库和专用用户，并导入表结构。本地和线上统一使用 `kungfu_game`，分别连接独立 MySQL 实例；不能通过同一实例的不同地址来隔离数据。
3. 创建 `dist/server`，在其中放入与这份客户端匹配的 `config.json`。

`config.json` 至少要有：

| 字段 | 怎么准备 |
| --- | --- |
| `config_hash` | 当前 `Data/config.spf2` 的 SHA-256，64 位小写十六进制 |
| `pools` | `模式:人数` → 地图 ID 数组，不能为空；从对应客户端核对 |
| `character_choices` | 创建角色的有效七槽候选，必须与客户端物品配置对应 |

不要用空 JSON 或随意编造编号来绕过检查。其他地图、奖励字段和数据库说明见 [服务端配置说明](server/go-server/README.md#准备数据库与配置)。服务器会建表，但**不会替你创建数据库**。

**完成条件：数据库可连接，`dist/server/config.json` 已准备好。**

## 第 3 步：构建并启动服务器

Windows CMD：

```bat
go -C server/go-server build -o ../../dist/server/kungfu-server.exe ./cmd/server
go -C server/go-server build -o ../../dist/server/kungfu-admin.exe ./cmd/admin
set "KK_MYSQL_DSN=kfo:替换为自己的密码@tcp(127.0.0.1:3306)/kungfu_game"
dist\server\kungfu-server.exe -config dist/server/config.json -cert-dir dist/server/certificates -listen 127.0.0.1:19090 -tls-listen 127.0.0.1:19091
```

Linux/macOS（bash/zsh）：

```sh
go -C server/go-server build -o ../../dist/server/kungfu-server ./cmd/server
go -C server/go-server build -o ../../dist/server/kungfu-admin ./cmd/admin
export KK_MYSQL_DSN='kfo:替换为自己的密码@tcp(127.0.0.1:3306)/kungfu_game'
./dist/server/kungfu-server -config dist/server/config.json -cert-dir dist/server/certificates -listen 127.0.0.1:19090 -tls-listen 127.0.0.1:19091
```

保持进程运行，在另一终端检查：

```text
curl http://127.0.0.1:19090/health
```

首次启动生成服务器 `origin.crt` 与 `origin.key`。登录器只需要服务器公有证书 `origin.crt`，不要分发 `origin.key`。`kungfu-admin` 是管理工具调用的程序，不需要单独保持运行。

**完成条件：健康检查返回 `status: ok`。这时才开始配置登录器。**

## 第 4 步：构建登录器，验证游戏登录

### 4.1 按依赖顺序构建（Windows）

在仓库根目录执行；先创建 `dist/launcher-components`：

```bat
go -C server/go-server build -o ../../dist/launcher-components/OnlineBridge.exe ./cmd/bridge
launcher\client-adapter\build-login-skin.cmd
dotnet publish tools/updater/Updater.csproj -c Release -o dist/updater
dotnet publish launcher/launcher-online/OnlineLauncher.csproj -c Release -o dist/launcher
```

顺序不能反：最后一步会嵌入前面构建的桥接 EXE、登录界面组件和更新助手。C++ 脚本默认使用 VS 2022 BuildTools 的安装路径，安装位置不同需调整脚本中的工具链路径。

产物：`dist/launcher/功夫小子登录器.exe`。本地/线上登录器来自同一个 C# 项目，并非两套源码；Go 桥接负责网络连接，C++ 组件负责游戏内登录界面。

### 4.2 把登录器放到游戏目录，准备连接配置

推荐运行目录：

```text
游戏目录/
  功夫小子登录器.exe
  bridge.json
  gfld.dat
  Data/                         # 自行提供的完整客户端资源
  certificates/                 # bridge.json 引用的证书
```

`bridge.json` 的主要字段：

| 字段 | 填什么 |
| --- | --- |
| `url` | 本地 `tls://127.0.0.1:19091`；线上示例 `wss://域名/kk/tunnel`，也支持部署好的直接 TLS 入口 |
| `client_directory` | 登录器与游戏同目录时填 `.` |
| `shared_client` | `true`，多个窗口共用同一份游戏资源 |
| `client_sha256` | `gfld.dat` 的 SHA-256 |
| `config_hash` | `Data/config.spf2` 的 SHA-256，与服务端一致 |
| `server_certificate` | 固定校验用的服务器公有证书 `origin.crt` 路径 |
| `login_certificate`、`login_key` | 桥接本地登录接口使用的匹配证书/私钥，**不是服务器 origin.key** |
| `trace_protocol` | 调试时可设为 `true` |

证书和哈希需由部署者准备，不能从别人的电脑直接照抄绝对路径。相对路径以配置文件目录为基准。登录器会准备内嵌组件并把原生登录需要的 `zz.crt` 放到游戏目录，玩家不用自己复制 DLL。

如果同时提供两个入口：线上入口读取 `bridge.json`；文件名带“本地”或“线下”的入口优先读取 `bridge.local.json`。两个配置使用同一客户端目录，分别配置连接地址和证书；本地游戏仍需要运行第 3 步的服务器。

### 4.3 启动并验收

双击登录器，填写账号密码并启动游戏。账号不存在时自动注册，随后进入取名/创建角色；已有账号校验原密码。每个窗口的账号密码在本机加密保存。

**完成条件：能进入大厅。** 卡在连接时先核对服务器是否运行、证书、哈希、目标地址及日志，不要先修改商城或战斗代码。详细配置、更新和多窗口行为见 [登录器说明](launcher/launcher-online/README.md)。

## 第 5 步：构建 GM，连接同一套数据

### 5.1 Windows 完整版：同一个 EXE 切换本地/线上

先构建 Flutter 界面；以下命令结束后回到仓库根目录：

```text
cd gm
flutter pub get
flutter build windows --release
cd ..
```

再构建管理后端到界面输出目录：

```text
go -C server/go-server build -ldflags "-H windowsgui" -o ../../gm/build/windows/x64/runner/Release/kungfu-desktop-admin.exe ./cmd/desktop-admin
```

将 `gm/build/windows/x64/runner/Release/` **整个目录**保存为 `dist/GM管理器/`。入口 `kungfu_item_manager.exe` 可以改名为 `GM管理器.exe`，但必须保留 DLL、`data/` 和 `kungfu-desktop-admin.exe`。

按 [GM 运行配置](gm/README.md#运行配置) 准备 `gm-settings.json`：

1. `root` 指向管理资源根目录，其中 `runtime-local/client` 提供道具与武器资源；可以链接到已有客户端，避免复制多份。
2. `local_settings` 指向含 `dsn`、`database` 的私有配置，本地模式连接第 2 步的独立库。GM 不负责启动数据库或建立 SSH 隧道。
3. 需要线上管理时，再配置 `root/runtime-local/online-admin.json` 和服务器端 `kungfu-admin`；仅本地测试可先不配置线上。

打开 GM，先选“本地测试服”，确认能读到刚才创建的账号，再设置奖励、商城或背包。Windows 完整版可以在同一个 EXE 内切换本地/线上；每次保存前核对目标环境。

**完成条件：GM 能读到目标环境的账号，保存后重新读取能看到修改。**

### 5.2 macOS / Android：后续需要时再构建

这两个平台使用 `lib/main_online.dart`，通过 HTTPS 管理 API 连接，不使用 Windows 的本机 Go EXE/SSH 模式。先部署 [GM HTTPS API](server/go-server/README.md#gm-https-管理接口)，再在 `gm` 目录运行 `flutter pub get` 和对应命令：

| 平台 | 构建环境 | 命令 | 输出 |
| --- | --- | --- | --- |
| macOS | Mac、Flutter、Xcode | `flutter build macos --release --target lib/main_online.dart` | `build/macos/Build/Products/Release/OpenKFO-GM.app` |
| Android APK | Windows/Linux/macOS、Flutter、Android SDK、兼容 JDK | `flutter build apk --release --target lib/main_online.dart` | `build/app/outputs/flutter-apk/app-release.apk` |
| Android AAB | 同上，自行配置发布签名 | `flutter build appbundle --release --target lib/main_online.dart` | `build/app/outputs/bundle/release/app-release.aab` |

启动后填写自己的 GM API 地址和管理令牌，不能填写游戏的 `/kk/tunnel` 地址。此入口不含本机武器资源编辑；Android 默认开发签名，macOS 构建和签名需在 Mac 上验证。详情见 [GM 平台说明](gm/README.md)。

## 第 6 步：按需要添加调试和更新工具

### 本地日志窗口（C#，可选）

```text
dotnet publish tools/local-server-monitor/LocalServerMonitor.csproj -c Release -r win-x64 --self-contained true -o dist/server
```

入口：`dist/server/本地服务器.exe`。Go 统一输出日志，窗口只读取本地文件，显示账号、玩家、方向、协议和原始 JSON/HEX，不向公网传送日志。

如果服务器按第 3 步启动：先创建 `dist/server/logs`，在启动命令追加 `-trace-protocol -protocol-log dist/server/logs/protocol-current.log`，窗口可接入该日志。

**窗口中的一键“启动服务器”走额外的 Windows 调试模式**，需要 `settings.private.json`、SSH 配置和独立测试库，不能把普通本机 MySQL 配置直接当成这个模式。配置见 [Windows 双击模式](server/go-server/README.md)，界面行为见 [日志窗口说明](tools/local-server-monitor/README.md)。

### 协议测试 APP（可选）

```text
go -C server/go-server build -o ../../dist/tester-components/ProtocolTesterCore.exe ./cmd/protocol-tester
dotnet publish launcher/protocol-tester/ProtocolTester.csproj -c Release -o dist/协议测试器
```

入口：`dist/协议测试器/协议测试器.exe`。先启动测试服，再选择本地登录器的 `bridge.json`，使用空闲测试账号；无需打开游戏即可测试收发协议。用例可能修改账号数据，使用独立测试库。详见 [测试器说明](launcher/protocol-tester/README.md)。

### 自动更新（可选）

登录器已在第 4 步嵌入独立更新助手；启动时检查登录器更新，启动游戏前检查客户端更新。需部署者另外配置发布服务，不是构建 EXE 就自动拥有更新源。

GM 需要自更新时，先构建助手，再把它连同整个 GM 目录一起发布：

```text
dotnet publish tools/updater/Updater.csproj -c Release -o dist/GM管理器
```

按自己的发布服务准备 `updater-settings.json`。只改本地客户端资源不等于线上更新完成；客户端、登录器和服务端允许的配置校验值必须一致。

## 给开发者和 AI 的最后检查

- 服务器逻辑改 `server/go-server`；登录器改 `launcher/launcher-online`；GM 界面改 `gm`；日志窗口改 `tools/local-server-monitor`。不要改错历史目录。
- Go 验证：仓库根目录运行 `go -C server/go-server test ./...`。数据库用例需另配 `KK_TEST_MYSQL_DSN` 或 `OPENKFO_DEBUG_DSN`，未配置会跳过，不能称为数据库验证通过。
- GM 验证：在 `gm` 目录运行 `flutter analyze`、`flutter test`。Windows/Linux 构建结果不代表 macOS/Android 实机验证。
- 源码、测试、构建描述提交 Git；真实连接配置、密钥、日志、数据库、游戏资源及 `dist/` 不提交。
- [实现清单](protocol/ImplementationBacklog.md) 记录缺口；[好友](protocol/FriendProtocol.md)、[背包校验](protocol/InventoryOwnershipChecks.md)、[练习 NPC](protocol/PracticeNPCProtocol.md)、[切换卡](protocol/WeaponSwitchCardProtocol.md) 说明近期实现边界。部分玩法仍待实现，不要把兼容服务器描述为原版完整复刻。


### 违禁词配置

服务器统一检查角色取名、改名、房间名称及公开/私聊发言，命中提示“违禁词！”。独立初始词库位于 `config/banned-words.txt`；GM 的“违禁词管理”支持搜索、添加、删除、批量粘贴和导入 UTF-8 TXT。保存到当前环境数据库后约 1 秒内生效，重启不会覆盖 GM 配置。

首次导入、数据库结构、词库匹配方式和来源见 [违禁词管理说明](docs/banned-words.md)。需要部署新版服务器及 GM 才能使用，旧运行进程不会自动获得这些改动。
