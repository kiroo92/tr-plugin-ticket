# 支持工单插件

- 独立公开源码仓库为 `kiroo92/tr-plugin-ticket`，主分支 `main`；Git 仅跟踪源码与构建配置，dist、release、依赖目录和 SQLite 业务文件不入库。
- 新建正式插件，对应 ../ticket-prototype/index.html；用户状态只有打开/已解决，管理端待回复为打开工单的额外标记。
- 使用上游 Go import 路径，通过 replace 固定 kiroo92/TokenRouter-plugin-data SDK v0.3.0；宿主至少 0.7.3，Init 必须校验 DataDir，业务存入 plugin.sqlite，不使用 KV、StateDir 或安装目录。
- 用户身份仅取 SDK caller；管理员端点也在处理器内验证角色。新建、回复、重开、解决需幂等，通知与业务在同一 SQLite 事务入库。
- 不提供附件上传。Telegram 使用宿主 secret 配置，消息不包含正文；失败持久重试，投递可能重复，不承诺仅一次。
- 实施进度与验证入口见 TODO.md；重型检查串行，GOMAXPROCS=2、GOFLAGS=-p=1、GOMEMLIMIT=1GiB。
- Windows 使用 `./build.ps1`；POSIX 使用 `make pack`。前端保持 Web SDK 0.2.0，Go CLI 使用模块 tool 依赖 `go tool trplugin`。打包图标源位于 `assets/menu.svg`（SDK 自动复制至 manifest 的 frontend/menu.svg）；Windows TMP/TEMP 须与输出在同一盘。
- SDK 定时任务最短为 1 分钟，通知任务每轮最多 5 条。修改状态必须联动待回复标记、消息、幂等记录和同事务通知；测试使用 plugintest 独立目录，先 Shutdown 再让 harness 清理。
