import { configureMock, HostRequestError } from '@tokenflux/tokenrouter-plugin-sdk/dev'
import type { Detail, Ticket } from './types'

const now = Date.now()
const ticket: Ticket = { id: 1, user_id: 1, username: 'Developer', title: 'API 调用出现 429，如何排查？', category: 'API 调用', priority: '普通', status: 'open', awaiting_admin: true, created_at: now - 3600000, updated_at: now }
const records = new Map<number, Detail>([[1, { ticket, messages: [
  { id: 1, role: 'user', author: 'Developer', body: '调用模型时偶尔出现 429。请问应该检查哪些配置？', created_at: now - 3600000 },
  { id: 2, role: 'staff', author: '支持团队', body: '请提供发生时间、模型名称及脱敏后的错误提示，方便我们继续排查。', created_at: now - 1800000 },
  { id: 3, role: 'user', author: 'Developer', body: '发生时间约为今天上午 10:30，已确认账户余额充足。', created_at: now },
], next_before: 0 }], [2, { ticket: { ...ticket, id: 2, username: 'noname@example.com', title: '账户名称未设置时的显示效果', category: '账户与权限', status: 'open', awaiting_admin: true, created_at: now - 600000, updated_at: now - 600000 }, messages: [
  { id: 4, role: 'user', author: 'noname@example.com', body: '这个账户没有设置显示名称，工单里改为显示邮箱。', created_at: now - 600000 },
], next_before: 0 }]])
const seen = new Map<string, { id: number }>()
let nextMessage = 5
configureMock(async (method, path, options) => {
  if (path.startsWith('/admin/notifications')) {
    if (method === 'GET') return { enabled: false, configured: false, pending: 0, failed: 0, recent: [] }
    throw new HostRequestError('本地演示不会发送 Telegram 消息', 409, 'MOCK_ONLY')
  }
  if (method === 'GET' && /^\/(admin\/)?tickets$/.test(path)) {
    const status = options?.query?.status ?? 'all'
    const query = options?.query?.q ?? ''
    const all = [...records.values()].map(value => value.ticket).sort((a, b) => b.updated_at - a.updated_at)
    const rows = all.filter(value => (status === 'all' || status === 'pending' && value.awaiting_admin && value.status === 'open' || value.status === status) && (value.title + value.id + records.get(value.id)!.messages.map(item => item.body).join(' ')).includes(query))
    return structuredClone({ rows, next_offset: -1, counts: { all: all.length, open: all.filter(value => value.status === 'open').length, resolved: all.filter(value => value.status === 'resolved').length, pending: all.filter(value => value.status === 'open' && value.awaiting_admin).length } })
  }
  const match = path.match(/^\/(admin\/)?tickets\/(\d+)(?:\/(reply|resolve|reopen))?$/)
  if (method === 'GET' && match) {
    const value = records.get(Number(match[2]))
    if (!value) throw new HostRequestError('工单不存在', 404, 'NOT_FOUND')
    return structuredClone(value)
  }
  if (method === 'POST') {
    const input = options?.body as Record<string, string>
    if (seen.has(input.request_id!)) return seen.get(input.request_id!)
    if (path === '/tickets') {
      const id = records.size + 1
      records.set(id, { ticket: { ...ticket, id, title: input.title!, category: input.category!, priority: input.priority!, status: 'open', awaiting_admin: true, created_at: Date.now(), updated_at: Date.now() }, messages: [{ id: nextMessage++, author: 'Developer', role: 'user', body: input.body!, created_at: Date.now() }], next_before: 0 })
      const result = { id }; seen.set(input.request_id!, result); return result
    }
    if (match) {
      const value = records.get(Number(match[2]))!
      if (!value) throw new HostRequestError('工单不存在', 404, 'NOT_FOUND')
      const action = match[3]
      value.ticket.updated_at = Date.now()
      if (action === 'reply') value.ticket.awaiting_admin = !match[1]
      else { value.ticket.status = action === 'resolve' ? 'resolved' : 'open'; value.ticket.awaiting_admin = action === 'reopen' && !match[1] }
      value.messages.push({ id: nextMessage++, author: 'Developer', role: action === 'reply' ? (match[1] ? 'staff' : 'user') : 'event', body: action === 'reply' ? input.body! : action === 'resolve' ? '工单已标记为已解决' : '工单已重新打开', created_at: Date.now() })
      const result = { id: value.ticket.id }; seen.set(input.request_id!, result); return result
    }
  }
  throw new HostRequestError('本地演示未实现此接口', 501, 'MOCK_ONLY')
})
