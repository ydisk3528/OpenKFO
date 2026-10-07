# 本轮修复概览

## 2026-10-06：修复自建武器应用时 buff 类型错误

- 根因：workspace 的 canonical `hit_properties` 从 stage 命中段继承了 XML 风格的 `"buff":"0"`，但 Go `HitProperty.Buff` 定义为 `int`，在进入归一化前就因 JSON 解码失败，报 `1008011.buff` 类型错误。
- 修复：Go `HitProperty.UnmarshalJSON` 兼容数字和数字字符串 buff，并对非法非整数保持显式报错；Dart `_canonicalMetadata` 在新快照中把整数型 buff 统一写成 `int`，不再生成字符串。
- 新增 `TestHitPropertyAcceptsLegacyStringBuff` 与 `TestWorkspaceAcceptsLegacyStringBuff`，覆盖底层解码和真实 workspace merge 入口。
- 验证：Go `internal/desktop` 定向测试和全量测试通过；两个相关 Dart 文件 LSP 无编译错误；AOT 验证构建通过。
- 本轮只生成 `gm/build/app-buff-verify.dill` 和 `gm/build/app.so` 验证产物，未安装到 Release/dist，未部署后端，未重启 GM/客户端/服务器，正式 `local-client/Data/config.spf2` 未修改。

## 2026-10-06：动作分支 stage 结构保留与验证状态

- 修复 `gm/lib/weapon_config.dart` 的 `commitRemap()`：当模板快照缺少 `property_ids` 或 `hits` 时，重建 stage 会保留当前 stage 的既有属性 ID 与命中段，并仅追加新属性，避免原属性在保存重映射后丢失。
- Go `internal/desktop` 全量定向测试通过；`weapon_config.dart` 与 `weapon_workspace.dart` LSP 均无编译错误；主应用 AOT 验证构建通过。
- 官方 Flutter 测试仍被 Windows `CreateFile failed 231` / `where.exe aapt` 阻断；最新自定义 widget runner 重新编译还受到 `platform_strong.dill` 的 Windows/MSYS 路径 URI 解析阻断，因此未将旧 runner 结果当作本轮最新源码回归结果。
- AOT 只生成 `gm/build/app-verify.dill` 与 `gm/build/app.so` 验证产物，未安装到 Release/dist；未部署后端、未重启正式 GM/客户端/服务器，正式 `local-client/Data/config.spf2` 未修改。
- 当前提交范围仍为 7 个源码/测试文件；`.workbuddy/`、`overview.md`、备份文件和 build 产物不提交。

## 最新交付：253451 多属性重映射与条件副本隔离回归

- 加固 `server/go-server/internal/desktop/weapon_workspace_archive_test.go`：逐个核对 `property_map` 的最终引用并集、供体属性号不残留、最终 `900000xxx` 独立号唯一登记且保留 `SkillDamage=1`，并按供体校验无条件/条件 `AnmDesc` 副本分布。
- 修复后的真实 `253451` 隔离归档测试通过：状态 `2012` 的 4 个命中属性分别映射为 workspace `800000055..058`，最终渲染为 `900000440..443`；条件副本完整，ElementTree 严格 XML 校验通过，commit 与重复 commit 均幂等，正式 `local-client` 字节未变化。
- 武器编辑器定向 Go 回归通过；Go 全量除无关的 `cmd/oss-sync` Windows 编译错误外均通过。该阻断为 `cmd/oss-sync/main.go:148` 引用未定义 `syscall.Stat_t`，本轮未改动。
- Dart 目标文件静态检查无编译错误，仅有既有 lint 警告；AOT 构建通过，`app.so` 已安装到 Release 与 `dist/GM管理器`，Material Icons 完整字体已确认。
- 尚未完成真实 Flutter 点击链路和完整 `weapon_apply` RPC 的 UI 隔离验收；本次验证覆盖 workspace、prepare、commit 及后端核心回归。

## 最新交付：修复命中属性 ID 混入 rules 的类型错误

- 根因是完整 Hit 对象曾被写入页面 `hitProperties`，随后直接投影到 `rules[*].properties[*]`；Go 的严格契约要求这里必须是 `map[string]map[string]float64`，因此 `id: "900000453"` 会触发 JSON 类型错误。
- `gm/lib/weapon_config.dart` 增加规则值收敛：完整 Hit 的 `id/values/buff/variant` 元数据不进入规则，数字字符串规范化为 JSON number；变体上下文、分支保存和规则投影统一走该收敛逻辑。
- `gm/lib/weapon_workspace.dart` 在 `fromPage` 快照入口再次净化规则属性，保证旧草稿或遗漏的 UI 路径也不会把完整 Hit 序列化进 `rules`；canonical `hit_properties` 仍保留完整 `id/values/references`。
- 回归夹具新增断言：规则 JSON 不含 `id/values/buff/variant`，canonical Hit 仍保留 `id` 与 `values`。
- Go `internal/desktop` 测试通过；Dart LSP 无编译错误；GM AOT 成功构建并安装到 Release 与 `dist/GM管理器`。
- 真实客户端隔离归档专项 `TestWeaponWorkspace253521ArchiveRoundTrip`、`TestWeaponWorkspace253521RemapExtraPropertyIsolation` 均通过，源 `local-client` 未被写入。
- Flutter widget 测试仍被 Windows `CreateFile failed 231`（`where.exe aapt` 管道资源耗尽）阻断，未进入测试用例；正式客户端 config/settings/基线未修改。

## 最新交付：新增 Buff 接入武器配置选择列表

- 定位到武器配置的 `weapon_catalog` / `weapon_detail` 只在基线归档上生成 `buffs` 和 `ustates`，没有叠加 settings 中的状态/Buff编辑集，因此新建 `435` 在状态页面可见，但武器配置的 DEBUFF 和自身状态下拉不可选。
- 修复 `server/go-server/internal/desktop/weapon.go`：武器读取投影在 `buildWeaponBase` 后统一调用 `applyBuffEdits`，让武器页读取当前状态编辑集中的新增/修改/删除状态。
- Go `internal/desktop` 完整测试通过；Flutter Buff、客户端配置、武器配置相关测试共 19 项通过；Dart LSP 无编译错误。
- AOT 已安装到 Release 与 `dist/GM管理器`；Go 后端已重新构建。正式客户端 `config.spf2`、settings、基线未修改。

## 最新交付：新建 Buff 保存按钮失效修复

- 定位到新建 `435` 刷新后消失的根因：`_save()` / `_saveLua()` 内部已经使用 `_run`，界面按钮又额外包了一层 `_run`；外层先设置 `busy=true`，内层命中 busy 守卫直接返回，导致 `weapon_buff_save` 从未发送，`settings.json` 也没有 `435`。
- 修复 `gm/lib/buff_config.dart` 的保存按钮回调，改为直接调用 `_save` / `_saveLua`，保留单层 busy、错误捕获和进度状态。
- 同时修复节点 XML 标题行在窄窗口下的 20px 横向溢出。
- 新增 `gm/test/buff_config_test.dart`，覆盖新建 `435`、点击保存并确认 `weapon_buff_save` 请求真实发出。
- Go `internal/desktop` 完整测试通过；Flutter Buff、客户端配置、武器配置相关测试共 19 项通过；Dart LSP 无编译错误。
- AOT 已安装到 Release 与 `dist/GM管理器`；Go 后端已重新构建。正式客户端 `config.spf2`、settings、基线未修改。

## 最新交付：状态/Buff 独立应用与说明编辑

- `weapon_buff_apply` 已改为 Buff-only 路径：从当前已应用客户端归档读取，叠加 `ustate.xml` 与 `script/playereventproc/ustateeventproc.lua`，不再重建武器 workspace，也不会触发 variants、rules、remaps 或预分配命中属性 ID 冲突。
- 已保留现有基线哈希、当前配置哈希、归档校验、自动备份和提交机制；应用成功后继续同步 `SourceHash` / `AppliedHash`。
- Buff 说明支持读取、编辑、保存和应用：前端新增“Buff 说明”输入框，后端更新 `<Data>` 节点前紧邻注释；新增状态仍保留默认说明逻辑。
- 新增已有状态说明注释更新测试；Go Buff 定向测试通过，Go `internal/desktop` 完整测试通过。
- Flutter `client_config_test.dart` 与 `weapon_config_test.dart` 共 18 项通过；Dart LSP 无编译错误；AOT 已安装到 Release 与 `dist/GM管理器`。
- Go desktop admin 已重新构建部署；未修改正式客户端 `config.spf2`、正式 settings、基线，也未重启用户 GM。

## 最新交付：分支命中属性同步与应用错误

- 修复分支 ID 重分配未预留内存对象的问题，避免 910000009 迁移时撞到已存在的 910000051；真实不同对象不盲目合并。
- VariantAnm.damage 支持数字和数字字符串，拒绝非法字符串。
- 分支与连招页共享命中参数，快照输出完整 HitProperty JSON，恢复兼容旧方案；新增、编辑、删除统一更新引用，删除命中保留纯动画段。
- 后端按状态投影属性，workspace 当前武器快照覆盖旧值、清理删除引用并保护其他武器。
- Go desktop 完整测试通过；Flutter weapon_config 14 项通过；LSP 无编译错误；AOT 构建安装通过。
- 真实 local-client 的 253521 两项隔离归档测试通过（61.253 秒）；正式 config.spf2/settings 未写入。
- 最新前后端安装到 gm/build/windows/x64/runner/Release 与 dist/GM管理器，两处哈希一致。
- 未重启用户 GM；当前 253450 用户方案的桌面与游戏实测仍需验收，不宣称已完成。

以下保留此前阶段记录。

## 统一命中属性模型第一阶段

- 新增 `server/go-server/internal/desktop/weapon_hit_property.go`，定义规范 `HitProperty` / `HitPropertyRef`。
- `weaponState` 增加 `HitProperties map[string]HitProperty`，统一保存命中属性 ID、模板来源、所属武器/状态/动作、条件、动作段、来源、编辑值、预分配标记和引用关系。
- workspace 合并后会从旧 `extra_properties`、`remaps`、`variants`、`rules.properties` 自动归一化；旧字段继续保留，兼容已有 settings 和旧 workspace JSON。
- Dart `WeaponWorkspace` 增加 `hit_properties` 保存/加载字段；旧方案不带该字段时仍可正常读取。
- 新增测试覆盖：分支与规则共享同一对象、引用去重、归一化幂等。

## 验证结果

- `go -C D:/OpenKFO/OpenKFO/server/go-server test ./internal/desktop -count=1`：通过。
- Dart LSP：无编译错误。
- AOT 构建：通过，并安装到：
  - `D:/OpenKFO/OpenKFO/gm/build/windows/x64/runner/Release/data/app.so`
  - `D:/OpenKFO/OpenKFO/dist/GM管理器/data/app.so`
- Go 后端构建并部署到：
  - `D:/OpenKFO/OpenKFO/gm/build/windows/x64/runner/Release/kungfu-desktop-admin.exe`
  - `D:/OpenKFO/OpenKFO/dist/GM管理器/kungfu-desktop-admin.exe`
- 两处后端 SHA256：`f98ad16abe3fcd3f59d6fef271f183ef42d6120f78c9a52eb92171879146edfa`
- 正式客户端 `config.spf2`、正式作者态和基线未写入。

## 当前边界与下一步

本阶段已将 `HitProperties` 接入应用前投影：统一对象的编辑值优先同步到现有 `Rule.Properties`，并新增统一对象删除、ID 重命名和全引用刷新；现有 `variants`、`rules`、`remaps` 渲染器继续作为兼容 XML 后端。下一阶段应让三个写回器直接消费 `HitProperties`，再完成有/无 `ustate` 两种 XML 输出的真实端到端测试。