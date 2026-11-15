import { test } from 'node:test'
import assert from 'node:assert/strict'
import { pendingKey, readPending } from './pending.mjs'

test('retries retain the exact request and isolate accounts and audiences', () => {
  const request = { path: '/tickets/42/reply', body: { request_id: 'fixed-request-00001', body: '未发送草稿' }, label: '发送回复' }
  const values = new Map([[pendingKey(11, false), JSON.stringify(request)]])
  const storage = { getItem: key => values.get(key) ?? null }
  assert.deepEqual(readPending(storage, pendingKey(11, false)), request)
  assert.equal(readPending(storage, pendingKey(12, false)), null)
  assert.equal(readPending(storage, pendingKey(11, true)), null)
  assert.throws(() => readPending({ getItem: () => '{' }, 'key'))
  for (const path of ['https://evil.test', '/admin/../tickets', '/tickets/1/delete']) {
    assert.throws(() => readPending({ getItem: () => JSON.stringify({ ...request, path }) }, 'key'))
  }
})
