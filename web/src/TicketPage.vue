<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { TrButton, TrDialog, TrEmpty, useHost } from '@tokenflux/tokenrouter-plugin-sdk'
import type { Detail, NoticeStatus, Pending, TicketList } from './types'
import { pendingKey, readPending } from './pending.mjs'

const props = defineProps<{ admin?: boolean }>()
const host = useHost()
const base = props.admin ? '/admin/tickets' : '/tickets'
const filter = ref('all')
const query = ref('')
const search = ref('')
const list = ref<TicketList>({ rows: [], next_offset: -1, counts: {} })
const detail = ref<Detail | null>(null)
const selected = ref<number | null>(null)
const loading = ref(false)
const detailLoading = ref(false)
const earlierLoading = ref(false)
const busy = ref(false)
const error = ref('')
const detailError = ref('')
const notice = ref<NoticeStatus | null>(null)
const noticeError = ref('')
const noticeOpen = ref(false)
const createOpen = ref(false)
const title = ref('')
const category = ref('API 调用')
const priority = ref('普通')
const body = ref('')
const reply = ref('')
const pending = ref<Pending | null>(null)
const storageError = ref('')
const filters = computed(() => props.admin ? [['all', '全部'], ['pending', '待管理员回复'], ['open', '打开'], ['resolved', '已解决']] : [['all', '全部'], ['open', '打开'], ['resolved', '已解决']])
const storageKey = computed(() => pendingKey(host.user.value?.id ?? 0, !!props.admin))
const disabled = computed(() => busy.value || !!pending.value || !!storageError.value)
const controller = new AbortController()
let listVersion = 0
let detailVersion = 0
let disposed = false
const message = (reason: unknown) => reason instanceof Error ? reason.message : '请求失败，请稍后重试'
const time = (stamp: number) => new Date(stamp).toLocaleString('zh-CN', { hour12: false })
const label = (state: string) => ({ pending: '等待发送', sent: '已发送', failed: '发送失败' }[state] ?? state)
const navigateTicket = (id: number) => host.navigate(import.meta.env.DEV ? (props.admin ? 'admin' : 'user') : '', { ticket: String(id) })

async function loadList(more = false) {
  const version = ++listVersion
  loading.value = true
  error.value = ''
  try {
    const result = await host.request<TicketList>('GET', base, { query: { status: filter.value, q: search.value, offset: String(more ? list.value.next_offset : 0) }, signal: controller.signal })
    if (version !== listVersion || disposed) return
    list.value = { ...result, rows: more ? [...list.value.rows, ...result.rows] : result.rows }
  } catch (reason) {
    if (!disposed && version === listVersion) error.value = message(reason)
  } finally { if (version === listVersion) loading.value = false }
}
async function loadDetail(id: number, earlier = false) {
  const version = ++detailVersion
  if (earlier) earlierLoading.value = true
  else { detailLoading.value = true; detail.value = null }
  detailError.value = ''
  try {
    const result = await host.request<Detail>('GET', base + '/' + id, { query: earlier ? { before: String(detail.value?.next_before) } : {}, signal: controller.signal })
    if (disposed || version !== detailVersion || selected.value !== id) return
    detail.value = earlier && detail.value ? { ...result, messages: [...result.messages, ...detail.value.messages] } : result
  } catch (reason) {
    if (!disposed && version === detailVersion) detailError.value = message(reason)
  } finally {
    if (version === detailVersion) { detailLoading.value = false; earlierLoading.value = false }
  }
}
async function choose(id: number) {
  if (selected.value !== id && reply.value.trim()) {
    if (!await host.confirm({ title: '切换工单', message: '当前回复尚未发送，确认放弃这段草稿并切换？' })) return
  }
  if (disposed) return
  if (selected.value !== id) reply.value = ''
  selected.value = id
  navigateTicket(id)
  await loadDetail(id)
}
async function loadNotice() {
  if (!props.admin) return
  noticeError.value = ''
  try {
    const result = await host.request<NoticeStatus>('GET', '/admin/notifications', { signal: controller.signal })
    if (!disposed) notice.value = result
  } catch (reason) { if (!disposed) noticeError.value = message(reason) }
}
async function sendPending() {
  const operation = pending.value
  if (!operation || busy.value) return
  busy.value = true
  error.value = ''
  let result: { id?: number }
  try {
    result = await host.request<{ id?: number }>('POST', operation.path, { body: operation.body, signal: controller.signal })
  } catch (reason) {
    if (!disposed) error.value = message(reason) + '。内容已保留，使用“重试原操作”可避免重复提交。'
    busy.value = false
    return
  }
  if (disposed) return
  try { sessionStorage.removeItem(storageKey.value) } catch { storageError.value = '操作已成功，但浏览器无法清除本地重试记录；再次重试仍会使用相同标识。' }
  if (!storageError.value) pending.value = null
  if (operation.path === '/tickets') { createOpen.value = false; title.value = ''; body.value = '' }
  if (operation.path.endsWith('/reply')) reply.value = ''
  host.toast.success(operation.path.includes('/notifications/') ? '测试通知已排队，将由后台任务发送' : '操作成功')
  busy.value = false
  if (result.id) { selected.value = result.id; navigateTicket(result.id); await loadDetail(result.id) }
  await loadList()
  if (props.admin) await loadNotice()
}
async function submit(path: string, values: Record<string, string>, actionLabel: string) {
  if (disabled.value) return
  if (!host.user.value) { error.value = '请登录后操作'; return }
  const operation = { path, body: { ...values, request_id: crypto.randomUUID() }, label: actionLabel }
  try { sessionStorage.setItem(storageKey.value, JSON.stringify(operation)) }
  catch { error.value = '浏览器无法保存重试记录，请允许此站点的会话存储后重试。内容尚未提交。'; return }
  pending.value = operation
  await sendPending()
}
async function discard() {
  if (!await host.confirm({ title: '清除重试记录', message: '原操作可能已在服务器成功。请先刷新并确认工单，清除后再次提交可能重复。草稿内容会保留。', danger: true })) return
  try { sessionStorage.removeItem(storageKey.value); pending.value = null; storageError.value = '' }
  catch { error.value = '浏览器无法清除记录，请检查站点存储权限' }
}
async function changeState() {
  if (!detail.value || disabled.value) return
  const action = detail.value.ticket.status === 'open' ? 'resolve' : 'reopen'
  const actionLabel = action === 'resolve' ? '标记已解决' : '重新打开'
  if (reply.value.trim()) { error.value = '请先发送或清空回复草稿，再变更状态'; return }
  if (await host.confirm({ title: actionLabel, message: action === 'resolve' ? '确认问题已解决？后续仍可重新打开。' : '重新打开后可以继续沟通。' })) {
    await submit(base + '/' + detail.value.ticket.id + '/' + action, {}, actionLabel)
  }
}
async function retryNotices() {
  if (disabled.value) return
  busy.value = true
  try { await host.request('POST', '/admin/notifications/retry', { signal: controller.signal }); if (!disposed) host.toast.success('失败通知已重新排队'); await loadNotice() }
  catch (reason) { if (!disposed) noticeError.value = message(reason) }
  finally { busy.value = false }
}
function applySearch() { search.value = query.value.trim(); void loadList() }
watch(filter, () => { void loadList() })
watch(() => host.route.value.query.ticket, value => {
  const id = Number(value)
  if (Number.isSafeInteger(id) && id > 0 && id !== selected.value) void choose(id)
})
onMounted(() => {
  try {
    pending.value = readPending(sessionStorage, storageKey.value)
    if (pending.value?.path === '/tickets') {
      title.value = pending.value.body.title ?? ''; body.value = pending.value.body.body ?? ''
      category.value = pending.value.body.category ?? 'API 调用'; priority.value = pending.value.body.priority ?? '普通'; createOpen.value = true
    } else if (pending.value?.path.endsWith('/reply')) reply.value = pending.value.body.body ?? ''
  } catch (reason) { storageError.value = message(reason) }
  const pendingId = pending.value?.path.match(/\/tickets\/(\d+)\//)?.[1]
  const id = Number(pendingId ?? host.route.value.query.ticket)
  if (Number.isSafeInteger(id) && id > 0) { selected.value = id; void loadDetail(id) }
  void loadList()
  if (props.admin) void loadNotice()
})
onBeforeUnmount(() => { disposed = true; controller.abort() })
</script>

<template>
  <main class="ticket-app">
    <header class="ticket-header">
      <div><p class="ticket-eyebrow">SUPPORT CENTER</p><h1>支持工单</h1><p class="ticket-muted">{{ admin ? '集中处理用户问题，让每一次反馈都有回应。' : '有问题，随时联系我们。进展和回复都会留在这里。' }}</p></div>
      <div class="ticket-actions"><TrButton v-if="admin" variant="secondary" @click="noticeOpen = !noticeOpen">Telegram 通知</TrButton><TrButton v-else :disabled="disabled" @click="createOpen = true">＋ 新建工单</TrButton><TrButton variant="ghost" :disabled="loading" @click="loadList(); selected && loadDetail(selected); admin && loadNotice()">刷新</TrButton></div>
    </header>
    <div v-if="pending || storageError" class="ticket-banner" role="status"><strong>{{ pending ? '待确认：' + pending.label : '重试记录不可用' }}</strong><span>{{ storageError || '上次操作尚未确认。原请求已保留，可安全重试；刷新工单可检查最新进展。' }}</span><details v-if="pending?.body.body"><summary>查看保留内容</summary><p class="ticket-preserve">{{ pending.body.title }}<br>{{ pending.body.body }}</p></details><div class="ticket-actions"><TrButton v-if="pending" :loading="busy" @click="sendPending">重试原操作</TrButton><TrButton variant="ghost" :disabled="busy" @click="discard">清除重试记录</TrButton></div></div>
    <p v-if="error" class="ticket-error" role="alert">{{ error }}</p>
    <section v-if="admin && noticeOpen" class="ticket-notice" aria-label="Telegram 通知状态">
      <div class="ticket-section-title"><div><h2>Telegram 工单通知</h2><p class="ticket-muted">在宿主的插件配置中设置 Bot Token、Chat ID 和网站 HTTPS 地址。新建、用户回复及重开后发送提醒。</p></div><span class="ticket-badge" :class="notice?.enabled ? 'is-open' : ''">{{ notice?.enabled ? '已启用' : '未启用' }}</span></div>
      <p class="ticket-muted">{{ notice?.pending ?? '—' }} 条等待发送 · {{ notice?.failed ?? '—' }} 条失败。后台每分钟处理，通知不含工单正文。</p>
      <div class="ticket-actions"><TrButton variant="secondary" :disabled="disabled || !notice?.enabled" @click="submit('/admin/notifications/test', {}, '测试通知')">发送测试通知</TrButton><TrButton variant="secondary" :disabled="disabled || !notice?.enabled || !notice?.failed" @click="retryNotices">重试失败通知</TrButton><TrButton variant="ghost" @click="loadNotice">刷新状态</TrButton></div>
      <p v-if="noticeError" class="ticket-error" role="alert">{{ noticeError }}</p>
      <ul v-if="notice?.recent.length" class="ticket-deliveries"><li v-for="item in notice.recent" :key="item.id"><span>{{ item.ticket_id ? '#TK-' + item.ticket_id : '测试通知' }}</span><span>{{ label(item.state) }} · 尝试 {{ item.attempts }} 次</span><span>{{ time(item.created_at * 1000) }}</span><span v-if="item.last_error" class="ticket-muted">{{ item.last_error }}</span></li></ul>
    </section>
    <nav class="ticket-tabs" aria-label="工单状态"><button v-for="[value, text] in filters" :key="value" :aria-current="filter === value ? 'page' : undefined" :class="{ active: filter === value }" @click="filter = value!">{{ text }} <span>{{ list.counts[value!] ?? 0 }}</span></button></nav>
    <form class="ticket-search" @submit.prevent="applySearch"><label for="ticket-search">搜索工单</label><input id="ticket-search" v-model="query" maxlength="200" placeholder="搜索标题、编号或对话内容"><TrButton variant="secondary" :loading="loading" @click="applySearch">搜索</TrButton></form>
    <div class="ticket-workspace">
      <section class="ticket-list" aria-label="工单列表" :aria-busy="loading">
        <p class="ticket-list-heading">{{ admin ? '用户工单' : '我的工单' }}<span>{{ loading ? '加载中…' : list.rows.length + ' 条已加载' }}</span></p>
        <TrEmpty v-if="!loading && !list.rows.length" title="暂无工单" description="可以换个筛选条件，或创建新的支持工单。" />
        <button v-for="ticket in list.rows" :key="ticket.id" class="ticket-row" :class="{ selected: selected === ticket.id }" @click="choose(ticket.id)">
          <span class="ticket-row-top"><span>#TK-{{ ticket.id }}</span><span class="ticket-badge" :class="ticket.status === 'open' ? 'is-open' : 'is-resolved'">{{ ticket.status === 'open' ? '打开' : '已解决' }}</span></span>
          <strong>{{ ticket.title }}</strong><span class="ticket-row-meta">{{ ticket.category }}<span v-if="ticket.priority === '紧急'" class="ticket-urgent">紧急</span><span v-if="admin && ticket.status === 'open' && ticket.awaiting_admin" class="ticket-pending-dot">待管理员回复</span></span>
          <span class="ticket-row-bottom"><span>{{ admin ? ticket.username : '最后更新' }}</span><time>{{ time(ticket.updated_at) }}</time></span>
        </button>
        <div v-if="list.next_offset >= 0" class="ticket-load"><TrButton variant="ghost" :loading="loading" @click="loadList(true)">加载更多工单</TrButton></div>
      </section>
      <section class="ticket-detail" aria-label="工单对话" :aria-busy="detailLoading">
        <p v-if="detailLoading" class="ticket-placeholder" role="status">正在加载工单…</p>
        <div v-else-if="detailError" class="ticket-placeholder"><p class="ticket-error" role="alert">{{ detailError }}</p><TrButton v-if="selected" variant="secondary" @click="loadDetail(selected)">重新加载</TrButton></div>
        <TrEmpty v-else-if="!detail" title="选择一条工单" description="在左侧查看并选择工单，在这里继续沟通。" />
        <template v-else>
          <div class="ticket-detail-heading"><div><p class="ticket-muted">#TK-{{ detail.ticket.id }} · {{ detail.ticket.category }} · {{ detail.ticket.priority }}</p><h2>{{ detail.ticket.title }}</h2><p v-if="admin" class="ticket-muted">{{ detail.ticket.username }} · 用户 #{{ detail.ticket.user_id }}</p></div><TrButton variant="secondary" :disabled="disabled" @click="changeState">{{ detail.ticket.status === 'open' ? '标记已解决' : '重新打开' }}</TrButton></div>
          <div v-if="admin && detail.ticket.status === 'open' && detail.ticket.awaiting_admin" class="ticket-waiting">待管理员回复 · 用户正在等待你的回应</div>
          <div class="ticket-thread"><div v-if="detail.next_before" class="ticket-load"><TrButton variant="ghost" :loading="earlierLoading" @click="loadDetail(detail.ticket.id, true)">加载更早的对话</TrButton></div><article v-for="item in detail.messages" :key="item.id" class="ticket-message" :class="'message-' + item.role"><div><strong>{{ item.role === 'staff' ? '管理员 · ' : '' }}{{ item.author }}</strong><time>{{ time(item.created_at) }}</time></div><p>{{ item.body }}</p></article></div>
          <form v-if="detail.ticket.status === 'open'" class="ticket-composer" @submit.prevent="submit(base + '/' + detail!.ticket.id + '/reply', { body: reply }, '发送回复')"><label for="ticket-reply">{{ admin ? '回复用户' : '补充回复' }}</label><textarea id="ticket-reply" v-model="reply" maxlength="4000" rows="4" :disabled="disabled" placeholder="补充问题细节或回复进展，请勿发送 API 密钥和密码。" /><div class="ticket-composer-bottom"><span class="ticket-muted">{{ reply.length }} / 4000 · 仅支持文字</span><TrButton :disabled="disabled || !reply.trim()" :loading="busy" @click="submit(base + '/' + detail!.ticket.id + '/reply', { body: reply }, '发送回复')">发送回复</TrButton></div></form>
          <div v-else class="ticket-resolved">✓ 工单已解决。需要继续沟通时，可以重新打开。</div>
        </template>
      </section>
    </div>
    <TrDialog :open="createOpen" title="新建支持工单" width="lg" @update:open="createOpen = $event">
      <form class="ticket-create" @submit.prevent="submit('/tickets', { title, category, priority, body }, '创建工单')"><p class="ticket-muted">清晰的问题描述，能帮助我们更快定位和处理。</p><label for="ticket-title">问题标题</label><input id="ticket-title" v-model="title" :disabled="disabled" maxlength="80" placeholder="用一句话描述遇到的问题"><div class="ticket-form-grid"><div><label for="ticket-category">问题类型</label><select id="ticket-category" v-model="category" :disabled="disabled"><option v-for="item in ['API 调用', '充值与账单', '福利活动', '账户与权限', '其他问题']" :key="item">{{ item }}</option></select></div><div><label for="ticket-priority">优先级</label><select id="ticket-priority" v-model="priority" :disabled="disabled"><option>普通</option><option>紧急</option></select></div></div><label for="ticket-body">问题描述</label><textarea id="ticket-body" v-model="body" :disabled="disabled" maxlength="4000" rows="7" placeholder="请描述操作步骤、错误提示和发生时间。请勿包含密码、API 密钥或其他敏感信息。" /><div class="ticket-composer-bottom"><span class="ticket-muted">{{ body.length }} / 4000 · 仅支持文字</span><TrButton v-if="pending?.path === '/tickets'" :loading="busy" @click="sendPending">重试提交</TrButton><TrButton v-else :disabled="disabled || !title.trim() || !body.trim()" :loading="busy" @click="submit('/tickets', { title, category, priority, body }, '创建工单')">提交工单</TrButton></div><p v-if="error" class="ticket-error" role="alert">{{ error }}</p><p v-if="pending" class="ticket-muted">原请求与内容已保留，可安全重试提交。</p></form>
    </TrDialog>
  </main>
</template>
