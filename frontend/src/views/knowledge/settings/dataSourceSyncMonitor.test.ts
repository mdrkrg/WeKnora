import assert from 'node:assert/strict'
import test from 'node:test'

import {
  isTargetSyncSettled,
  monitorDataSourceSync,
} from './dataSourceSyncMonitor.ts'

test('only treats the requested terminal sync log as settled', () => {
  assert.equal(isTargetSyncSettled({ id: 'old', status: 'success' }, 'new'), false)
  assert.equal(isTargetSyncSettled({ id: 'new', status: 'running' }, 'new'), false)
  assert.equal(isTargetSyncSettled({ id: 'new', status: 'failed' }, 'new'), false)
  assert.equal(isTargetSyncSettled({ id: 'new', status: 'success' }, 'new'), true)
})

test('refreshes knowledge until a running sync succeeds', async () => {
  const snapshots = [
    { latest_sync_log: { id: 'sync-1', status: 'running' } },
    { latest_sync_log: { id: 'sync-1', status: 'success' } },
  ]
  let refreshes = 0
  const result = await monitorDataSourceSync({
    targetSyncLogId: 'sync-1',
    fetchDataSource: async () => snapshots.shift()!,
    refreshKnowledge: async () => { refreshes++ },
    wait: async () => {},
  })

  assert.equal(result, 'success')
  assert.equal(refreshes, 2)
})

test('keeps monitoring a failed log because the queue may retry it', async () => {
  const snapshots = [
    { latest_sync_log: { id: 'sync-1', status: 'failed' } },
    { latest_sync_log: { id: 'sync-1', status: 'success' } },
  ]
  const result = await monitorDataSourceSync({
    targetSyncLogId: 'sync-1',
    fetchDataSource: async () => snapshots.shift()!,
    refreshKnowledge: async () => {},
    wait: async () => {},
  })

  assert.equal(result, 'success')
})

test('waits past an older terminal log until the requested sync appears', async () => {
  const snapshots = [
    { latest_sync_log: { id: 'old', status: 'success' } },
    { latest_sync_log: { id: 'sync-1', status: 'partial' } },
  ]
  const result = await monitorDataSourceSync({
    targetSyncLogId: 'sync-1',
    fetchDataSource: async () => snapshots.shift()!,
    refreshKnowledge: async () => {},
    wait: async () => {},
  })

  assert.equal(result, 'partial')
})
