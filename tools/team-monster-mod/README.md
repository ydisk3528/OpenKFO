# 本地团队怪物 MOD 实验工具

源码原工作目录为 `E:\功夫小子\keji\KK_Science_Exe`，本目录保存本次可构建版本。
运行 `build.cmd` 使用 VS 2022 Community x86 工具链构建；预览图片已包含，不需要重新提取游戏资源。
`render_previews.py` 仅用于开发时重新生成图片，要求仓库旁存在 `client/Data` 和仓库内的 Go SDK。

启动线下服务器前，在同一个 PowerShell 中设置：

```powershell
$env:OPENKFO_NEUTRAL_NPC_EXPERIMENT = '1'
```

服务器实验功能仅支持回环地址。所有参战客户端须在本机；房主请求生成，双方完成确认后，再由房主启用 AI。
可选恶棍、熊，每批 1–4 只。熊保留协议模板编号 2。只支持经过哈希检查的客户端版本。

构建后可运行 `--self-test` 和 `--ui-self-test`，两者不操作游戏进程。
`inspect_neutral_targets.py PID` 为只读诊断，不读取真实 Lua 目标或血量，不能仅凭快照断言攻击命中。

2026-09-24 实测：双方生成两只怪物；房主 test001 收到来自怪物 100/101 的伤害事件；用户随后确认靠近的另一方也可被攻击。尚未完成房主同队队友的专项验证。本轮目标诊断没有修改原版 AI 筛选逻辑。
