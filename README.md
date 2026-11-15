# 支持工单插件

独立的 TokenRouter 工单插件，插件 ID 为 `tokenflux.ticket`。从现有工单原型实现，提供用户和管理页面，不修改宿主、福利插件或订单通知插件。

## 自建来源安装

在管理员插件管理的来源设置中添加下列来源，启用并刷新后，在发现页安装“支持工单”。分发方式与福利插件一致，索引使用独立 Ed25519 签名身份。

- 名称：支持工单 · kiroo92
- 来源 URL：`https://github.com/kiroo92/tr-plugin-ticket/releases/latest/download/index.json`
- Ed25519 公钥：`wTa5qI3iRwrJE70I8459VZrTyYREWuD3k1jmlbBPCyA=`
- 发布页：https://github.com/kiroo92/tr-plugin-ticket/releases

宿主须能访问 GitHub Releases 及发行资产下载主机。索引和 index.json.sig 必须来自同一次发布，安装包链接固定到版本标签。发布时先完整上传草稿并校验，再公开；不替换已公开的同版本资产。私钥仅保存在本机用户配置目录，不进入 Git 或发布资产。

## 功能

- 用户：创建本人工单、搜索标题/编号/对话、查看历史、回复、标记解决和重新打开。
- 用户筛选只有“全部 / 打开 / 已解决”。管理员可查看所有用户工单，另有“待管理员回复”筛选和计数。
- 业务状态只有打开、已解决。新建、用户回复或用户重新打开会进入待管理员回复；管理员回复或解决会清除该标记。管理员重新打开时不自动设置待回复。
- 问题类型：API 调用、充值与账单、福利活动、账户与权限、其他问题；优先级：普通/紧急，仅用于展示，不承诺处理时效。
- 无附件上传、退款或钱包操作。正文为纯文字，标题最长 80 字、消息最长 4000 字。列表每页 30 条，对话每页 50 条。
- 每次写操作携带请求标识，服务端事务保存结果；同一账户的同一请求重试返回原结果，不重复写入。浏览器会话存储保留待确认操作，刷新后需手动重试。未提交的普通草稿不跨刷新保留；关闭标签会清除会话存储，清除记录后应先核对服务器记录，避免重复提交。

## 依赖与宿主

需要本仓库 fork 的 TokenRouter **v0.7.3 或更高兼容版本**，不能仅凭上游宿主版本号判断具备持久目录能力。Init 同时检查绝对路径 DataDir、目录可用性、数据库可打开并可初始化。

Go SDK 保留上游导入：

```go
require github.com/TokenFlux/TokenRouter/plugin-sdk/go v0.2.1
replace github.com/TokenFlux/TokenRouter/plugin-sdk/go => github.com/kiroo92/TokenRouter-plugin-data/plugin-sdk/go v0.3.0
tool github.com/TokenFlux/TokenRouter/plugin-sdk/go/cmd/trplugin
```

使用 `go list -m -json github.com/TokenFlux/TokenRouter/plugin-sdk/go` 检查 Replace 指向 fork v0.3.0。SDK 要求 Go 1.27.3；Web SDK 保持 0.2.0。SQLite 使用插件自行引入的纯 Go 驱动 modernc.org/sqlite，原生构建无需 CGO。

## 数据、升级与备份

所有业务数据位于宿主提供的 `DataDir/plugin.sqlite`，包括工单、消息、幂等操作和 Telegram 发件箱。使用 WAL、FULL 同步、外键和单连接访问；连接重建后仍启用外键、忙等待及同步设置。不调用宿主 KV，不在 StateDir 或安装目录写业务数据。Init 拒绝空/相对/不存在的目录、异常数据库路径、损坏或更高版本的数据库，不做目录回退。

宿主普通停用、重启、升级、回滚和卸载会保留 DataDir；显式清除插件数据才删除。本插件不执行清除。Shutdown 取消投递任务，等待正在处理的请求/任务，再关闭数据库。旧插件进程结束前不要让另一个实例访问同一目录。

备份建议：在宿主停用插件并确认进程退出后，备份整个 DataDir，再启动插件；恢复也在停用时进行。不要在运行时仅复制 plugin.sqlite，否则可能遗漏 WAL 中的数据。宿主配置和加密密钥须另行备份，Bot Token 不在 SQLite 中。数据库当前 schema 为 1；不支持数据库降级、跨节点共享目录或网络文件系统。单实例本地持久卷是当前运行方式。

这是从内存原型新建的插件，没有历史 KV 数据需要迁移。文件大小取决于业务增长，没有应用级自动清理或宿主 KV 配额限制；请监控磁盘并定期备份。宿主的数据导出大小限制仍可能影响大数据库，届时停用插件后在卷层备份。

## Telegram 配置

在宿主“插件 → 支持工单 → 配置”填写：

| 字段 | 内容 |
| --- | --- |
| telegram_enabled | 是否启用，默认关闭 |
| bot_token | BotFather 的 Bot Token，宿主 secret 字段；不回传前端，不落 SQLite |
| chat_id | 接收消息的数字 Chat ID；群组通常为负数；机器人需具备发送权限 |
| site_url | 用户能访问的网站 HTTPS 根地址，例如 https://example.com，不带子路径、参数或账号 |

新工单、用户回复、用户重开时在**同一业务事务**写入发件箱。通知只含编号、类型、事件和受权限保护的管理端链接，不含用户名、标题或正文。接收者仍需登录管理员账号才能查看。只有管理员能查询投递状态、排队测试通知和重试失败通知。

SDK 定时任务最短间隔为 1 分钟，每次最多发送 5 条、每次 HTTP 最长 8 秒。429 尊重 retry_after（最多 24 小时）；网络、无效响应和服务器错误指数退避，最多失败 10 次转为失败；无效权限等永久错误直接失败。管理端可以重新排队失败项。排队不等于已送达，可刷新通知面板查看最近 20 条状态。

通知采用至少一次投递：进程在 Telegram 接收后、数据库确认前退出可能产生重复提醒。停用通知期间不新增通知，既有待发送项暂停；重新启用后继续。更改 Chat ID 或网站地址仅影响新入队项，已有项保留原目标；投递使用当前 Bot Token。群组变更时应核对剩余队列。点击测试通知会真实发送到配置会话，本地演示不会发送。

## 构建与校验

始终限制资源：`GOMAXPROCS=2`、`GOFLAGS=-p=1`、`GOMEMLIMIT=1GiB`，重型命令串行。

Windows PowerShell 7.4+：

```powershell
.\build.ps1
```

脚本依次运行 Go 测试/vet、锁定安装前端依赖、前端小测试、类型检查与构建，再生成 Linux amd64/arm64、macOS amd64/arm64、Windows amd64 五个平台二进制，以 `go tool trplugin` 打包并逐一验证。环境变量在结束后恢复；Windows 打包临时目录位于项目 dist/tmp，避免跨盘重命名失败。前端建议使用 .node-version 指定的 Node 和 pnpm 12.9.1。

Linux/macOS：`make pack`；Makefile 的 `TRPLUGIN ?= go tool trplugin` 使用模块锁定的 SDK。CI 仅验证，运行 Linux 竞态检查与完整打包，不上传或发布插件。

产物：`release/tokenflux.ticket-0.1.0-<platform>.tar.gz`，在目标宿主插件管理中选择对应平台包安装。不要将服务器数据库混入发布包。

## 开发与验证边界

`pnpm --dir web dev --host 127.0.0.1` 打开本地界面；顶部 SDK 工具栏可选 user/admin。演示为内存数据，刷新重置、角色选择无安全意义、不会发送真实 TG，生产包不包含模拟处理器。生产身份只取 SDK caller；后端再次检查管理员权限及用户归属，未知字段拒绝写入，未授权工单返回 404。

Go 测试使用 `plugintest.New(t).Env`，插件资源在 harness 清理目录前关闭。覆盖工单状态与权限、正文边界、搜索与分页、并发幂等、通知事务回滚、DataDir 重开、不同 harness 隔离、连接重建外键、TG 本地服务成功/限流/失败/恢复、停机取消。前端小测试验证重试记录的请求不变、账户和页面隔离、损坏记录拒绝。

本地检查及未验证范围详见 TODO.md。真实宿主安装/升级/卸载/清除及真实 Telegram 仍需在测试环境联调；无线上部署或官方仓库发布。
