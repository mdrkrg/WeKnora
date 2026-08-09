import { get, getDown } from '@/utils/request'
import { buildArtifactQuery, type ArtifactQueryParams } from '@/utils/knowledgeArtifacts'
import type { ArtifactListItem, ArtifactReadResponse } from '@/types/knowledgeArtifact'

/** GET /api/v1/knowledge/:id/artifact —— 读取单个产物内容。 */
export function readArtifact(
  knowledgeId: string,
  params: ArtifactQueryParams = {},
): Promise<ArtifactReadResponse> {
  return get(`/api/v1/knowledge/${knowledgeId}/artifact${buildArtifactQuery(params)}`)
}

/** GET /api/v1/knowledge/:id/artifacts —— 产物元信息列表（无内容）。 */
export function listArtifacts(
  knowledgeId: string,
  params: { attempt?: number } = {},
): Promise<ArtifactListItem[]> {
  return get(`/api/v1/knowledge/${knowledgeId}/artifacts${buildArtifactQuery(params)}`)
}

/** GET /api/v1/knowledge/:id/artifact/download —— 流式下载产物。 */
export function downloadArtifact(
  knowledgeId: string,
  params: ArtifactQueryParams = {},
): Promise<Blob> {
  return getDown(`/api/v1/knowledge/${knowledgeId}/artifact/download${buildArtifactQuery(params)}`)
}
