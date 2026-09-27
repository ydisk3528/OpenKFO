# Redis 业务接入：排行榜

本次实现排行榜共享缓存。没有迁移登录令牌、房间状态、资产、结算或战斗同步，也没有部署游戏服务器。

原实现每次读取全部账号 profile 并排序。现在每个分类先查询进程内快照，再查询 Redis，最后查询 MySQL；分类内并发请求合并。快照有效期 30 秒，Redis 命中不会重新延长快照期限。排名、昵称及新注册账号进入榜单可能延迟最多 30 秒。返回前 100 名和玩家自己的排名，保持原协议布局及同分 UID 排序。

Redis 使用 go-redis v9.7.3，RESP2，已在现有 Redis 7.2.14 上验证。Redis 单次命令上下文、连接、读写及连接池等待上限为 100ms，不自动重试；错误后 5 秒跳过 Redis。缓存损坏、写失败或断连不会改变数据库结果。数据库读取有 5 秒上下文超时。排行榜等待走现有 storage lane，不持有大厅状态锁执行网络读写；不引入战斗 Redis 锁。

## 启用

仅游戏服务器 `serve` 操作读取：

```ini
OPENKFO_REDIS_ADDR=127.0.0.1:6379
OPENKFO_REDIS_PASSWORD_FILE=/root/.openkfo-redis-password
OPENKFO_REDIS_NAMESPACE=online
```

密码文件仅服务运行用户可读，不把密码写到 Git、命令行或日志。线下必须使用另一 namespace，例如 `offline`；不同数据库也必须隔离 namespace。地址为空则不连接 Redis，仍使用进程内缓存。填写地址但 namespace 无效或密码文件无法读取，会明确报配置错误；运行中 Redis 不可达则自动回退。

键为 `openkfo:<namespace>:rankings:v1:<category>`，TTL 30 秒，仅含 UID、昵称编码、分数和快照过期时间。Redis 是可丢弃缓存，MySQL 是来源；无数据库结构迁移。部署新二进制和环境配置并重启游戏服后生效，客户端无需更新。回退时移除 Redis 地址并重启即可；不需要清空整个 Redis。

## 已验证

- 40 个并发请求只加载一次数据，过期重新加载。
- 不同玩家共享榜单但自己的排名分别正确，非法分类仍拒绝。
- Redis 不可达时数据库回退及短暂熔断；数据库失败不会伪装成功。
- 真实 Redis：不同 Store 共享快照、TTL、损坏数据重建、线上线下 namespace 隔离。测试使用独立临时键并清理，不读写玩家数据库。

启动日志的 `configured` 只表示已配置，不等于连接成功。故障摘要 `ranking_cache Redis unavailable` 表示已经回退。当前不会用 Redis 限流故障阻止玩家登录，也不会用 Redis 代替资产事务幂等记录。

## 本机线下接入（2026-09-27）

复用本机安装的 Windows 移植版 Redis 8.10.1，监听 127.0.0.1:6380，1 GiB maxmemory，noeviction。仅存放可重建排行缓存，关闭 RDB/AOF。密码与配置位于 runtime-local/redis-offline（ASCII junction E:/OpenKFO-Redis-Offline），目录仅本用户/SYSTEM访问。
本地工作进程在无参数启动时读取同目录 redis.local.json，设置排行榜环境；未运行时启动指定 Redis。CLI/线上入口不受影响。命名空间 offline_kungfu_game；集成自测使用独立临时键。密码文件不打包/提交。
实际 Redis PING、内存配置、缓存集成/损坏回退测试通过；先停 Redis，再由本地日志窗口在独立测试库启动服务，自动启动 Redis、健康检查、防重复和正常停止通过。两个 EXE 已备份替换到 server/dist/local-server。
