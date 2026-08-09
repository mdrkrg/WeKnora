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

export function isCanvasAuthorizationError(connectorType: string, error: unknown): boolean {
  if (connectorType !== 'canvas') return false

  const message = dataSourceErrorMessage(error).toLowerCase()
  if (!message) return false

  return (
    message.includes('invalid credentials')
    || message.includes('invalid access token')
    || message.includes('unauthorized')
    || /(?:access|refresh)[ _-]?token.*(?:expired|invalid|revoked)/.test(message)
  )
}
