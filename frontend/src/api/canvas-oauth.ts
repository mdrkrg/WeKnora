import { get } from '@/utils/request'

export interface CanvasOAuthStatus {
  configured: boolean
  base_url?: string
  client_id?: string
}

export async function getCanvasOAuthStatus(): Promise<CanvasOAuthStatus> {
  const response: any = await get('/api/v1/canvas/oauth/status')
  if (response && typeof response.configured === 'boolean') {
    return response as CanvasOAuthStatus
  }
  if (response?.success && response?.data) {
    return response.data as CanvasOAuthStatus
  }
  return { configured: false }
}
