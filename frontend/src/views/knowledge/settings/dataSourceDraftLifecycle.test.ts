import assert from 'node:assert/strict'
import test from 'node:test'

import {
  deleteTemporaryDataSourceDraft,
  shouldDeleteTemporaryDataSource,
} from './dataSourceDraftLifecycle.ts'

// Characterization tests for the current drawer lifecycle. The first case is
// the "configuration disappears" candidate: a data source row created early
// (e.g. paused for resource listing) must be cleaned up when a new drawer is
// closed before final submit.
test('new drawer with an unsaved temporary data source triggers cleanup', () => {
  assert.equal(
    shouldDeleteTemporaryDataSource({ isEdit: false, tempDsId: 'ds-temp', isCommitted: false }),
    true,
  )
})

test('final submit clears temp id before close, so cleanup is skipped', () => {
  assert.equal(
    shouldDeleteTemporaryDataSource({ isEdit: false, tempDsId: '', isCommitted: true }),
    false,
  )
})

test('closing an edit drawer never cleans up the existing data source', () => {
  assert.equal(
    shouldDeleteTemporaryDataSource({ isEdit: true, tempDsId: 'ds-existing', isCommitted: false }),
    false,
  )
})

test('a committed temporary id is never deleted during close', () => {
  assert.equal(
    shouldDeleteTemporaryDataSource({ isEdit: false, tempDsId: 'ds-saved', isCommitted: true }),
    false,
  )
})

test('draft cleanup invokes delete exactly once and returns the deleted id', async () => {
  const deleted: string[] = []
  const id = await deleteTemporaryDataSourceDraft(
    { isEdit: false, tempDsId: 'ds-temp', isCommitted: false },
    async value => { deleted.push(value) },
  )
  assert.equal(id, 'ds-temp')
  assert.deepEqual(deleted, ['ds-temp'])
})

test('draft cleanup keeps the id available to the caller when deletion fails', async () => {
  await assert.rejects(
    deleteTemporaryDataSourceDraft(
      { isEdit: false, tempDsId: 'ds-temp', isCommitted: false },
      async () => { throw new Error('delete failed') },
    ),
    /delete failed/,
  )
})
