# v0.1.0

- 首次发布支持工单：用户新建、搜索、回复、解决与重开；管理员待回复筛选及处理。
- 独立 SQLite 数据库位于 DataDir/plugin.sqlite，不使用宿主 KV；支持幂等重试、重启恢复和权限隔离。
- Telegram 提醒使用持久发件箱，支持重试、状态查询和测试通知；后台每分钟处理，通知不包含工单正文。
- 适配 kiroo92/TokenRouter-plugin-data 发行的 TokenRouter v0.7.3+ 与 Go SDK v0.3.0。
- 提供五个平台安装包和独立签名安装来源，来源 URL 与公钥见 README.md。

真实宿主生命周期与真实 Telegram 联网尚未联调。通知为至少一次投递，异常恢复可能重复提醒；请使用持久卷并定期备份。
