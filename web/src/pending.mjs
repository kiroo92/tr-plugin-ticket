export function pendingKey(userId, admin) {
  return 'tokenflux.ticket:pending:' + userId + ':' + (admin ? 'admin' : 'user')
}
export function readPending(storage, key) {
  const raw = storage.getItem(key)
  if (!raw) return null
  const value = JSON.parse(raw)
  const validPath = typeof value?.path === 'string' && (
    /^\/(admin\/)?tickets(\/\d+\/(reply|resolve|reopen))?$/.test(value.path) ||
    value.path === '/admin/notifications/test'
  )
  if (!validPath || typeof value.label !== 'string' || !value.body ||
      typeof value.body.request_id !== 'string' || !/^[A-Za-z0-9_-]{16,100}$/.test(value.body.request_id) ||
      Object.values(value.body).some(entry => typeof entry !== 'string')) {
    throw new Error('待重试操作损坏，请保留草稿并清除这条操作')
  }
  return value
}
