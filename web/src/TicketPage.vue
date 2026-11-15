<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
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
const mobileChat = ref(false)
const title = ref('')
const category = ref('API 调用')
const priority = ref('普通')
const body = ref('')
const reply = ref('')
const pending = ref<Pending | null>(null)
const storageError = ref('')
const replyBox = ref<HTMLTextAreaElement | null>(null)
const filters = computed(() => props.admin ? [['all', '全部'], ['pending', '待回复'], ['open', '打开'], ['resolved', '已解决']] : [['all', '全部'], ['open', '打开'], ['resolved', '已解决']])
const storageKey = computed(() => pendingKey(host.user.value?.id ?? 0, !!props.admin))
const disabled = computed(() => busy.value || !!pending.value || !!storageError.value)
const current = computed(() => detail.value?.ticket ?? null)
const threadMessages = computed(() => detail.value?.messages ?? [])
const nextBefore = computed(() => detail.value?.next_before ?? 0)
const controller = new AbortController()
let listVersion = 0
let detailVersion = 0
let disposed = false
let searchTimer: ReturnType<typeof setTimeout> | undefined
const message = (reason: unknown) => reason instanceof Error ? reason.message : '请求失败，请稍后重试'
const time = (stamp: number) => new Date(stamp).toLocaleString('zh-CN', { hour12: false, month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
const day = (stamp: number) => new Date(stamp).toLocaleString('zh-CN', { hour12: false, month: 'numeric', day: 'numeric' })
const stamp = (stamp: number) => Date.now() - stamp < 86400000 ? new Date(stamp).toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' }) : day(stamp)
const label = (state: string) => ({ pending: '等待发送', sent: '已发送', failed: '发送失败' }[state] ?? state)
const excerpt = (text: string) => text.replace(/\s+/g, ' ').trim()
const navigateTicket = (id: number) => host.navigate(import.meta.env.DEV ? (props.admin ? 'admin' : 'user') : '', { ticket: String(id) })

async function loadList(more = false) {
  const version = ++listVersion
  loading.value = true
  error.value = ''
  try {
    const result = await host.request<TicketList>('GET', base, { query: { status: filter.value, q: search.value, offset: String(more ? list.value.next_offset : 0) }, signal: controller.signal })
    if (version !== listVersion || disposed) return
    list.value = { ...result, rows: more ? [...list.value.rows, ...result.rows] : result.rows }
    if (!more && selected.value === null && result.rows.length && !host.route.value.query.ticket) {
      selected.value = result.rows[0].id
      void loadDetail(result.rows[0].id)
    }
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
    await nextTick()
    resizeReply()
    scrollThread(false)
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
  mobileChat.value = true
  navigateTicket(id)
  await loadDetail(id)
  void nextTick(() => document.querySelector('.tk-workspace')?.scrollIntoView({ block: 'nearest' }))
}
async function loadNotice() {
  if (!props.admin) return
  noticeError.value = ''
  try {
    const result = await host.request<NoticeStatus>('GET', '/admin/notifications', { signal: controller.signal })
    if (!disposed) notice.value = result
  } catch (reason) { if (!disposed) noticeError.value = message(reason) }
}
async function refreshAll() {
  await loadList()
  if (selected.value) await loadDetail(selected.value)
  if (props.admin) await loadNotice()
}
function resizeReply() {
  const box = replyBox.value
  if (!box) return
  box.style.height = 'auto'
  box.style.height = Math.min(Math.max(box.scrollHeight, 40), 132) + 'px'
}
function scrollThread(smooth: boolean) {
  const thread = document.querySelector<HTMLElement>('.tk-thread')
  if (thread) thread.scrollTo({ top: thread.scrollHeight, behavior: smooth ? 'smooth' : 'auto' })
}
function onReplyKey(event: KeyboardEvent) {
  if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
    event.preventDefault()
    if (current.value && reply.value.trim() && !disabled.value) void sendReply()
  }
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
  if (result.id) { selected.value = result.id; mobileChat.value = true; navigateTicket(result.id); await loadDetail(result.id) }
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
async function sendReply() {
  if (!current.value || !reply.value.trim()) return
  await submit(base + '/' + current.value.id + '/reply', { body: reply.value }, '发送回复')
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
watch(query, () => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(applySearch, 300)
})
watch(filter, () => { void loadList() })
watch(reply, () => { void nextTick(resizeReply) })
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
  if (Number.isSafeInteger(id) && id > 0) { selected.value = id; mobileChat.value = !!pendingId; void loadDetail(id) }
  void loadList()
  if (props.admin) void loadNotice()
})
onBeforeUnmount(() => { disposed = true; if (searchTimer) clearTimeout(searchTimer); controller.abort() })
</script>

<template>
  <main class="ticket-app">
    <header class="tk-appbar">
      <div class="tk-title">
        <span class="tk-mark" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M2 9a3 3 0 0 1 0 6v2a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-2a3 3 0 0 1 0-6V7a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2Z"/><path d="M13 5v2M13 17v2M13 11v2"/></svg></span>
        <h1>支持工单</h1>
        <p class="tk-sub">{{ admin ? '集中查看用户问题，及时回复并跟进解决' : '你的问题，在这里持续跟进' }}</p>
      </div>
      <div class="tk-actions">
        <TrButton v-if="admin" variant="ghost" @click="noticeOpen = true"><span class="tk-notice-label">通知<svg class="tk-bell" viewBox="0 0 24 24" aria-hidden="true"><path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9"/><path d="M10 21h4"/></svg><i v-if="notice?.enabled" class="tk-dot" aria-hidden="true"></i></span></TrButton>
        <TrButton variant="ghost" :disabled="loading" @click="refreshAll">刷新</TrButton>
        <TrButton v-if="!admin" :disabled="disabled" @click="createOpen = true">＋ 新建工单</TrButton>
      </div>
    </header>

    <div v-if="pending || storageError" class="tk-banner" role="status">
      <div class="tk-banner-text"><strong>{{ pending ? '待确认：' + pending.label : '重试记录不可用' }}</strong><span>{{ storageError || '上次操作尚未确认。原请求已保留，可安全重试；刷新工单可检查最新进展。' }}</span><details v-if="pending?.body.body"><summary>查看保留内容</summary><p class="tk-preserve">{{ pending.body.title }}<br>{{ pending.body.body }}</p></details></div>
      <div class="tk-actions"><TrButton v-if="pending" size="sm" :loading="busy" @click="sendPending">重试原操作</TrButton><TrButton variant="ghost" size="sm" :disabled="busy" @click="discard">清除记录</TrButton></div>
    </div>
    <p v-if="error" class="tk-error" role="alert">{{ error }}</p>

    <div class="tk-toolbar">
      <nav class="tk-filters" aria-label="工单状态">
        <button v-for="[value, text] in filters" :key="value" type="button" :aria-pressed="filter === value" @click="filter = value!">{{ text }}<b>{{ list.counts[value!] ?? 0 }}</b></button>
      </nav>
      <label class="tk-search">
        <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/></svg>
        <span class="tk-sr-only">搜索工单</span>
        <input v-model="query" maxlength="200" type="search" placeholder="搜索标题、编号或内容" @keydown.esc="query = ''; applySearch()">
      </label>
    </div>

    <div class="tk-workspace" :class="{ 'show-chat': mobileChat }">
      <aside class="tk-panel tk-list-panel" aria-label="工单列表">
        <div class="tk-list-head"><span>{{ admin ? '全部工单' : '我的工单' }}</span><span>{{ loading ? '加载中…' : list.rows.length + ' 条' }}</span></div>
        <div class="tk-tickets" :aria-busy="loading">
          <TrEmpty v-if="!loading && !list.rows.length" title="暂无工单" description="换个筛选条件，或创建新的支持工单。" />
          <button v-for="ticket in list.rows" :key="ticket.id" type="button" class="tk-ticket" :class="{ selected: selected === ticket.id }" :aria-pressed="selected === ticket.id" @click="choose(ticket.id)">
            <span class="tk-ticket-top"><span class="tk-ticket-id">#TK-{{ ticket.id }}<template v-if="admin"> · {{ ticket.username }}</template></span><span v-if="admin && ticket.status === 'open' && ticket.awaiting_admin" class="tk-badge pending">待回复</span><span v-else class="tk-badge" :class="ticket.status === 'open' ? 'open' : 'resolved'">{{ ticket.status === 'open' ? '打开' : '已解决' }}</span></span>
            <span class="tk-ticket-title">{{ ticket.title }}</span>
            <span class="tk-ticket-foot"><span class="tk-ticket-cat">{{ ticket.category }}<template v-if="ticket.priority === '紧急'"> · <em>紧急</em></template></span><time>{{ stamp(ticket.updated_at) }}</time></span>
          </button>
          <div v-if="list.next_offset >= 0" class="tk-load"><TrButton variant="ghost" size="sm" :loading="loading" @click="loadList(true)">加载更多</TrButton></div>
        </div>
      </aside>

      <section class="tk-panel tk-chat" aria-label="工单对话" :aria-busy="detailLoading">
        <p v-if="detailLoading" class="tk-chat-state" role="status">正在加载工单…</p>
        <div v-else-if="detailError" class="tk-chat-state"><p class="tk-error" role="alert">{{ detailError }}</p><TrButton variant="secondary" size="sm" @click="selected && loadDetail(selected)">重新加载</TrButton></div>
        <div v-else-if="!current" class="tk-chat-state"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M21 15a2 2 0 0 1-2 2H8l-5 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2Z"/></svg><b>选择一条工单</b><span>在左侧选择工单，查看完整对话与处理进展。</span></div>
        <template v-else>
          <header class="tk-chat-head">
            <div class="tk-chat-main">
              <div class="tk-chat-meta">
                <button type="button" class="tk-back" @click="mobileChat = false">← 返回</button>
                <span class="tk-ticket-id">#TK-{{ current.id }}</span>
                <span class="tk-badge" :class="current.status === 'open' ? 'open' : 'resolved'">{{ current.status === 'open' ? '打开' : '已解决' }}</span>
                <span v-if="current.priority === '紧急'" class="tk-urgent">紧急</span>
                <span class="tk-meta-cat">{{ current.category }}</span>
              </div>
              <h2>{{ current.title }}</h2>
              <p class="tk-chat-sub"><span>{{ current.category }}</span><span>提交人 <b>{{ current.username }}</b></span><span class="tk-sub-secondary">创建于 {{ time(current.created_at) }}</span><span>更新于 {{ time(current.updated_at) }}</span></p>
            </div>
            <div class="tk-actions"><TrButton variant="secondary" size="sm" :disabled="disabled" @click="changeState">{{ current.status === 'open' ? '标记解决' : '重新打开' }}</TrButton></div>
          </header>
          <div v-if="admin && current.status === 'open' && current.awaiting_admin" class="tk-waiting">用户正在等待管理员回复</div>
          <div class="tk-thread">
            <div v-if="nextBefore" class="tk-load"><TrButton variant="ghost" size="sm" :loading="earlierLoading" @click="loadDetail(current.id, true)">加载更早的对话</TrButton></div>
            <template v-for="item in threadMessages" :key="item.id">
              <div v-if="item.role === 'event'" class="tk-event">{{ item.body }} · {{ time(item.created_at) }}</div>
              <div v-else class="tk-msg" :class="item.role === 'staff' ? 'staff' : 'user'">
                <span class="tk-avatar" aria-hidden="true">{{ item.role === 'staff' ? '客' : item.author.slice(0, 1).toUpperCase() }}</span>
                <div class="tk-msg-main">
                  <div class="tk-msg-head"><b>{{ item.role === 'staff' ? '客服 · ' + item.author : item.author }}</b><span v-if="item.role === 'staff'" class="tk-role">客服</span><time>{{ stamp(item.created_at) }}</time></div>
                  <div class="tk-bubble">{{ item.body }}</div>
                </div>
              </div>
            </template>
          </div>
          <form v-if="current.status === 'open'" class="tk-composer" @submit.prevent="sendReply">
            <div class="tk-composer-box">
              <textarea id="ticket-reply" ref="replyBox" v-model="reply" maxlength="4000" rows="1" :disabled="disabled" :placeholder="admin ? '填写回复，告知用户处理进展…' : '补充问题细节，或回复客服…'" aria-label="回复内容" @keydown="onReplyKey" />
              <div class="tk-composer-bar"><span class="tk-hint">Ctrl + Enter 发送 · 请勿包含密钥或密码</span><span class="tk-count">{{ reply.length }} / 4000</span><TrButton size="sm" :disabled="disabled || !reply.trim()" :loading="busy" @click="sendReply">发送</TrButton></div>
            </div>
          </form>
          <div v-else class="tk-resolved"><span>✓ 工单已解决。需要继续沟通时，可以重新打开。</span><TrButton variant="secondary" size="sm" :disabled="disabled" @click="changeState">重新打开</TrButton></div>
        </template>
      </section>
    </div>

    <TrDialog :open="createOpen" title="新建支持工单" width="md" @update:open="createOpen = $event">
      <form class="ticket-create" @submit.prevent="submit('/tickets', { title, category, priority, body }, '创建工单')">
        <p class="tk-dialog-hint">描述清楚现象和影响，方便我们尽快定位。</p>
        <label class="tk-field" for="ticket-title"><span>问题标题</span><input id="ticket-title" v-model="title" :disabled="disabled" maxlength="80" placeholder="用一句话概括问题"></label>
        <div class="tk-field-row">
          <label class="tk-field" for="ticket-category"><span>问题类型</span><select id="ticket-category" v-model="category" :disabled="disabled"><option v-for="item in ['API 调用', '充值与账单', '福利活动', '账户与权限', '其他问题']" :key="item">{{ item }}</option></select></label>
          <label class="tk-field" for="ticket-priority"><span>优先级</span><select id="ticket-priority" v-model="priority" :disabled="disabled"><option>普通</option><option>紧急</option></select></label>
        </div>
        <label class="tk-field" for="ticket-body"><span>问题描述</span><textarea id="ticket-body" v-model="body" :disabled="disabled" maxlength="4000" rows="5" placeholder="发生了什么、你期望的结果是什么？可附上操作步骤、发生时间和请求 ID。" /></label>
        <p class="tk-note"><b>请勿提交</b> API 密钥、密码或完整支付信息。</p>
        <p v-if="error" class="tk-error" role="alert">{{ error }}</p>
        <p v-if="pending" class="tk-muted">原请求与内容已保留，可安全重试提交。</p>
        <div class="tk-dialog-foot"><TrButton variant="ghost" :disabled="busy" @click="createOpen = false">取消</TrButton><TrButton v-if="pending?.path === '/tickets'" :loading="busy" @click="sendPending">重试提交</TrButton><TrButton v-else :disabled="disabled || !title.trim() || !body.trim()" :loading="busy" @click="submit('/tickets', { title, category, priority, body }, '创建工单')">提交工单</TrButton></div>
      </form>
    </TrDialog>

    <TrDialog :open="noticeOpen" title="Telegram 通知" width="sm" @update:open="noticeOpen = $event">
      <section class="ticket-notice" aria-label="Telegram 通知状态">
        <p class="tk-dialog-hint">在宿主的插件配置中设置 Bot Token、Chat ID 和网站 HTTPS 地址。新建、用户回复及重开后发送提醒。</p>
        <p class="tk-notice-state"><span class="tk-badge" :class="notice?.enabled ? 'open' : ''">{{ notice?.enabled ? '已启用' : '未启用' }}</span><span>{{ notice?.pending ?? '—' }} 条等待发送 · {{ notice?.failed ?? '—' }} 条失败</span></p>
        <p class="tk-muted">后台每分钟处理，通知不含工单正文。</p>
        <div class="tk-actions tk-notice-actions"><TrButton variant="secondary" size="sm" :disabled="disabled || !notice?.enabled" @click="submit('/admin/notifications/test', {}, '测试通知')">发送测试通知</TrButton><TrButton variant="secondary" size="sm" :disabled="disabled || !notice?.enabled || !notice?.failed" @click="retryNotices">重试失败通知</TrButton><TrButton variant="ghost" size="sm" @click="loadNotice">刷新状态</TrButton></div>
        <p v-if="noticeError" class="tk-error" role="alert">{{ noticeError }}</p>
        <ul v-if="notice?.recent.length" class="tk-deliveries"><li v-for="item in notice.recent" :key="item.id"><span>{{ item.ticket_id ? '#TK-' + item.ticket_id : '测试通知' }}</span><span>{{ label(item.state) }} · 尝试 {{ item.attempts }} 次</span><time>{{ time(item.created_at * 1000) }}</time><span v-if="item.last_error" class="tk-muted">{{ item.last_error }}</span></li></ul>
      </section>
    </TrDialog>
  </main>
</template>
