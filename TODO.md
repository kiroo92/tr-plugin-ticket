# 支持工单实施

## 目标与边界
新建独立工单插件，保留原型业务流程；使用 fork SDK v0.3.0、DataDir/plugin.sqlite，不用 KV，不修改宿主或其他插件。用户后续已授权发布 Git，源码发布到 kiroo92/tr-plugin-ticket；不部署宿主，不发布到 TokenFlux 官方仓库。Go 重型检查串行，GOMAXPROCS=2、GOFLAGS=-p=1、GOMEMLIMIT=1GiB。

## 验收与步骤
- [x] T1 核对 SDK 接口并固定依赖、CLI、manifest。
- [x] T2 完成 SQLite 工单、权限、幂等和 TG 持久通知及测试。
- [x] T3 完成 Vue 用户/管理页面和构建文档。
- [x] T4 测试、前端构建、跨平台打包和 SDK 校验；记录竞态检查限制。

## 恢复点
正式实现和本机验收已完成：18 项 Go 测试通过，go vet 通过；前端重试记录测试、vue-tsc 和 Vite 构建通过；五平台二进制均编译成功，五个 tar.gz 安装包均由 fork SDK 的 go tool trplugin 校验通过。最终 build.ps1 在包含弹窗重试入口的代码上完整重跑成功。

浏览器已验证用户创建/回复/解决、管理员待回复筛选和回复、通知面板、390px 深色布局（无整页横向溢出）、刷新恢复原请求、页面和弹窗直接重试后的清理。浏览器诊断无页面错误或失败请求（仅 Vite 调试连接信息）。

本机竞态检查尝试失败：CGO_ENABLED=1 时缺少 gcc；Linux CI 已配置 go test -race，但未在本机执行 CI。真实 v0.7.3 宿主生命周期和 Telegram 联网未验证；没有部署宿主或发布到官方仓库。
