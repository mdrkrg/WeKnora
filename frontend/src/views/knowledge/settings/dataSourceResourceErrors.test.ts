import assert from 'node:assert/strict'
import test from 'node:test'

import {
  dataSourceErrorMessage,
  isCanvasAuthorizationError,
} from './dataSourceResourceErrors.ts'

test('extracts API messages from flat and nested errors', () => {
  assert.equal(dataSourceErrorMessage({ message: 'flat' }), 'flat')
  assert.equal(dataSourceErrorMessage({ error: 'string error' }), 'string error')
  assert.equal(dataSourceErrorMessage({ error: { message: 'nested' } }), 'nested')
})

test('recognizes rejected Canvas access tokens as authorization errors', () => {
  assert.equal(
    isCanvasAuthorizationError('canvas', {
      status: 400,
      message: 'invalid credentials: invalid access token',
    }),
    true,
  )
  assert.equal(
    isCanvasAuthorizationError('canvas', {
      message: 'refresh_token expired or revoked',
    }),
    true,
  )
})

test('does not mistake Canvas connectivity failures for expired authorization', () => {
  assert.equal(
    isCanvasAuthorizationError('canvas', {
      status: 400,
      message: 'failed to fetch items from source: connect: connection refused',
    }),
    false,
  )
})

test('does not apply Canvas authorization rules to another connector', () => {
  assert.equal(
    isCanvasAuthorizationError('feishu', { message: 'invalid access token' }),
    false,
  )
})
