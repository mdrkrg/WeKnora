// 与 internal/types/artifact.go 的 JSON 字段对齐
// 产物类型常量（对应后端 types.ArtifactType*）
export const ARTIFACT_TYPE_MARKDOWN = 'markdown'
export const ARTIFACT_TYPE_IMAGE_MANIFEST = 'image_manifest'
export const ARTIFACT_TYPE_ENGINE_NATIVE = 'engine_native'

export interface ArtifactReadResponse {
  knowledge_id: string
  parse_attempt: number
  engine: string
  artifact_type: string
  native_kind?: string
  format: string
  sha256: string
  size: number
  content?: string
}

export interface ArtifactListItem {
  artifact_type: string
  native_kind?: string
  format: string
  sha256: string
  size: number
  created_at: string
}
