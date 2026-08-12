import assert from 'node:assert/strict'
import test from 'node:test'

import {
  dataSourceErrorMessage,
  isAuthorizationError,
} from './dataSourceResourceErrors.ts'

test('extracts API messages from flat and nested errors', () => {
  assert.equal(dataSourceErrorMessage({ message: 'flat' }), 'flat')
  assert.equal(dataSourceErrorMessage({ error: 'string error' }), 'string error')
  assert.equal(dataSourceErrorMessage({ error: { message: 'nested' } }), 'nested')
})

test('recognizes rejected access tokens as authorization errors', () => {
  assert.equal(
    isAuthorizationError('oauth-connector', {
      status: 400,
      message: 'invalid credentials: invalid access token',
    }),
    true,
  )
  assert.equal(
    isAuthorizationError('oauth-connector', {
      message: 'refresh_token expired or revoked',
    }),
    true,
  )
})

test('does not mistake connectivity failures for expired authorization', () => {
  assert.equal(
    isAuthorizationError('oauth-connector', {
      status: 400,
      message: 'failed to fetch items from source: connect: connection refused',
    }),
    false,
  )
})

test('does not apply authorization rules to an opted-out connector', () => {
  assert.equal(
    isAuthorizationError('', { message: 'invalid access token' }),
    false,
  )
})
