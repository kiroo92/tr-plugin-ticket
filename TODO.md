# 支持工单实施

## 目标与边界
新建独立工单插件，保留原型业务流程；使用 fork SDK v0.3.0、DataDir/plugin.sqlite，不用 KV，不修改宿主或其他插件。用户后续已授权发布 Git，源码发布到 kiroo92/tr-plugin-ticket；不部署宿主，不发布到 TokenFlux 官方仓库。Go 重型检查串行，GOMAXPROCS=2、GOFLAGS=-p=1、GOMEMLIMIT=1GiB。

## 验收与步骤
- [x] T1 核对 SDK 接口并固定依赖、CLI、manifest。
- [x] T2 完成 SQLite 工单、权限、幂等和 TG 持久通知及测试。
- [x] T3 完成 Vue 用户/管理页面和构建文档。
- [x] T4 测试、前端构建、跨平台打包和 SDK 校验；记录竞态检查限制。

## v0.2.0 界面重做

- [x] 按 redesign 原型重写 TicketPage.vue 结构与 style.css，版本升至 0.2.0。
- [x] vue-tsc、前端测试、Vite 构建通过；18 项 Go 测试与 go vet 通过。
- [x] build.ps1 完整重跑：五平台编译、打包、SDK validate 全部通过。
- [x] Vite dev 实测：用户/管理端、发送回复后待回复计数归零、深色模式、390px 手机对话与自动定位、弹窗整宽输入框。
- [ ] 安装到真实 v0.7.3 宿主复验；发布 v0.2.0 索引需另行签名上传。

## 恢复点
正式实现和本机验收已完成：18 项 Go 测试通过，go vet 通过；前端重试记录测试、vue-tsc 和 Vite 构建通过；五平台二进制均编译成功，五个 tar.gz 安装包均由 fork SDK 的 go tool trplugin 校验通过。最终 build.ps1 在包含弹窗重试入口的代码上完整重跑成功。

浏览器已验证用户创建/回复/解决、管理员待回复筛选和回复、通知面板、390px 深色布局（无整页横向溢出）、刷新恢复原请求、页面和弹窗直接重试后的清理。浏览器诊断无页面错误或失败请求（仅 Vite 调试连接信息）。

本机竞态检查因缺少 gcc 无法执行；GitHub Actions 的 go test -race ./... 步骤已通过（运行 43673740368）。真实 v0.7.3 宿主生命周期和 Telegram 联网未验证；没有部署宿主或发布到官方仓库。

用户要求发布时同时交付与福利相同的自建安装来源。v0.1.0 索引使用独立 ticket-index 身份签名，安装包固定版本地址、五平台 SHA-256 和原始 manifest 哈希均已核对，签名验证及篡改拒绝检查通过。URL 与公钥见 README；按完整草稿核验后公开的流程发布。
