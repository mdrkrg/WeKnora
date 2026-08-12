export function dataSourceErrorMessage(error: unknown): string {
  if (typeof error === 'string') return error
  if (!error || typeof error !== 'object') return ''

  const value = error as Record<string, unknown>
  if (typeof value.message === 'string') return value.message
  if (typeof value.error === 'string') return value.error
  if (value.error && typeof value.error === 'object') {
    const nested = value.error as Record<string, unknown>
    if (typeof nested.message === 'string') return nested.message
  }
  return ''
}

// isAuthorizationError reports whether the error message signals an expired or
// revoked authorization (invalid/expired token, invalid credentials) for the
// given connector type. Connectors that use per-user authorization (OAuth2)
// pass their type; others pass '' to opt out entirely.
export function isAuthorizationError(connectorType: string, error: unknown): boolean {
  if (!connectorType) return false

  const message = dataSourceErrorMessage(error).toLowerCase()
  if (!message) return false

  return (
    message.includes('invalid credentials')
    || message.includes('invalid access token')
    || message.includes('unauthorized')
    || /(?:access|refresh)[ _-]?token.*(?:expired|invalid|revoked)/.test(message)
  )
}
