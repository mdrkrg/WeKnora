// Artifact 相关纯函数：查询串构造、尺寸格式化、类型标签、下载文件名。
// 与组件解耦以便单测（tsx --test）。

import {
  ARTIFACT_TYPE_ENGINE_NATIVE,
  ARTIFACT_TYPE_IMAGE_MANIFEST,
  ARTIFACT_TYPE_MARKDOWN,
} from '../types/knowledgeArtifact'

const FORMAT_EXTENSION_MAP: Record<string, string> = {
  markdown: 'md',
  json: 'json',
}

export interface ArtifactQueryParams {
  type?: string
  native_kind?: string
  attempt?: number
  resolve_images?: boolean
}

/** 构造产物接口的查询串；无参数时返回空串（与后端 form 绑定字段一致）。 */
export function buildArtifactQuery(params: ArtifactQueryParams = {}): string {
  const query = new URLSearchParams()
  if (params.type) query.set('type', params.type)
  if (params.native_kind) query.set('native_kind', params.native_kind)
  if (params.attempt) query.set('attempt', String(params.attempt))
  if (params.resolve_images) query.set('resolve_images', 'true')
  const qs = query.toString()
  return qs ? `?${qs}` : ''
}

/** 字节数格式化：B / KB / MB / GB。 */
export function formatArtifactSize(bytes: number): string {
  if (bytes >= 1073741824) return `${(bytes / 1073741824).toFixed(1)} GB`
  if (bytes >= 1048576) return `${(bytes / 1048576).toFixed(1)} MB`
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${bytes} B`
}

/** 产物类型展示名；未知类型回退为原始值。 */
export function artifactTypeLabel(artifactType: string): string {
  switch (artifactType) {
    case ARTIFACT_TYPE_MARKDOWN:
      return 'Markdown'
    case ARTIFACT_TYPE_IMAGE_MANIFEST:
      return 'Image Manifest'
    case ARTIFACT_TYPE_ENGINE_NATIVE:
      return 'Engine Native'
    default:
      return artifactType
  }
}

/**
 * 下载文件名。后端 Content-Disposition 固定为 "artifact"，必须由前端
 * 按产物元信息命名：engine_native 带 native_kind（JSON 结构），其余按
 * format 取扩展名。
 */
export function artifactFileName(item: {
  artifact_type: string
  native_kind?: string
  format?: string
}): string {
  if (item.native_kind) {
    return `${item.artifact_type}-${item.native_kind}.json`
  }
  const extension = FORMAT_EXTENSION_MAP[item.format || ''] || item.format || 'bin'
  return `${item.artifact_type}.${extension}`
}
