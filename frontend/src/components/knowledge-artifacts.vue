<script setup lang="ts">
// 解析产物（markdown / image_manifest / engine_native）查看器。
// 懒挂载：由父组件在切换到 artifact 视图时才渲染本组件。
// 数据只取最新解析尝试（attempt=0 即后端 current attempt）。
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import mermaid from 'mermaid'
import 'katex/dist/katex.min.css'
import 'highlight.js/styles/github.css'

import { downloadArtifact, listArtifacts, readArtifact } from '@/api/knowledge-artifacts'
import { ARTIFACT_TYPE_MARKDOWN } from '@/types/knowledgeArtifact'
import type { ArtifactListItem } from '@/types/knowledgeArtifact'
import {
  artifactFileName,
  artifactTypeLabel,
  formatArtifactSize,
} from '@/utils/knowledgeArtifacts'
import { renderKnowledgeMarkdown } from '@/utils/knowledgeMarkdownRenderer'

const { t } = useI18n()

const props = defineProps<{
  knowledgeId: string
}>()

const artifacts = ref<ArtifactListItem[]>([])
const loading = ref(false)
const loadError = ref('')

const viewingType = ref('')
const viewingNativeKind = ref('')
const viewingContent = ref('')
const contentLoading = ref(false)
const contentError = ref('')
const contentOversized = ref(false)

const resolveImages = ref(false)

const processedMarkdown = computed(() =>
  viewingType.value === ARTIFACT_TYPE_MARKDOWN
    ? renderKnowledgeMarkdown(viewingContent.value)
    : '',
)

// 后端内联内容上限 10 MiB；超限时返回 400 提示走下载接口。
const OVERSIZED_MARKER = 'too large'

async function fetchArtifacts() {
  if (!props.knowledgeId) return
  loading.value = true
  loadError.value = ''
  viewingContent.value = ''
  viewingType.value = ''
  viewingNativeKind.value = ''
  contentError.value = ''
  contentOversized.value = false
  resolveImages.value = false
  try {
    const list = await listArtifacts(props.knowledgeId)
    artifacts.value = list
    if (list.length === 0) return
    const markdown = list.find((a) => a.artifact_type === ARTIFACT_TYPE_MARKDOWN)
    if (markdown) {
      await viewContent(markdown.artifact_type, markdown.native_kind)
    }
  } catch (e: any) {
    loadError.value = e?.message || t('knowledgeBase.artifactLoadError')
  } finally {
    loading.value = false
  }
}

async function viewContent(artifactType: string, nativeKind?: string) {
  contentLoading.value = true
  contentError.value = ''
  contentOversized.value = false
  viewingType.value = artifactType
  viewingNativeKind.value = nativeKind || ''
  try {
    const resp = await readArtifact(props.knowledgeId, {
      type: artifactType,
      ...(nativeKind ? { native_kind: nativeKind } : {}),
      ...(resolveImages.value ? { resolve_images: true } : {}),
    })
    viewingContent.value =
      resp.content || JSON.stringify(resp, null, 2)
  } catch (e: any) {
    if (e?.status === 400 && typeof e?.message === 'string' && e.message.includes(OVERSIZED_MARKER)) {
      contentOversized.value = true
    } else {
      contentError.value = e?.message || t('knowledgeBase.artifactContentLoadError')
    }
  } finally {
    contentLoading.value = false
  }
}

async function handleDownload(item: ArtifactListItem) {
  try {
    const blob = await downloadArtifact(props.knowledgeId, {
      type: item.artifact_type,
      ...(item.native_kind ? { native_kind: item.native_kind } : {}),
      ...(resolveImages.value ? { resolve_images: true } : {}),
    })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = artifactFileName(item)
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('knowledgeBase.artifactDownloadError'))
  }
}

async function onResolveImagesChange() {
  if (viewingType.value) {
    await viewContent(viewingType.value, viewingNativeKind.value || undefined)
  }
}

// Mermaid 渲染（与 doc-content 的 post-render 管线一致）
async function renderMermaidDiagrams() {
  const root = previewRoot.value
  if (!root) return
  const nodes = root.querySelectorAll('.mermaid')
  if (nodes.length === 0) return
  try {
    await mermaid.run({ nodes: Array.from(nodes) as HTMLElement[] })
  } catch (e) {
    console.error('[artifact] mermaid rendering error:', e)
  }
}

const previewRoot = ref<HTMLElement | null>(null)

watch(processedMarkdown, async () => {
  if (!processedMarkdown.value) return
  await nextTick()
  await renderMermaidDiagrams()
})

onMounted(() => {
  fetchArtifacts()
})

watch(() => props.knowledgeId, () => {
  fetchArtifacts()
})
</script>

<template>
  <div class="artifact-body">
    <div v-if="loading" class="artifact-loading">
      <t-loading size="small" />
      <span>{{ $t('knowledgeBase.artifactLoading') }}</span>
    </div>

    <div v-else-if="loadError" class="artifact-error">
      <t-icon name="error-circle" size="14px" />
      <span>{{ loadError }}</span>
    </div>

    <div v-else-if="!artifacts.length" class="artifact-empty">
      {{ $t('knowledgeBase.artifactNoArtifacts') }}
    </div>

    <template v-else>
      <div class="artifact-toolbar">
        <t-switch
          v-if="viewingType === ARTIFACT_TYPE_MARKDOWN"
          size="small"
          v-model="resolveImages"
          :label="$t('knowledgeBase.artifactResolveImages')"
          @change="onResolveImagesChange"
        />
      </div>

      <div class="artifact-list">
        <div
          v-for="item in artifacts"
          :key="item.artifact_type + (item.native_kind || '')"
          class="artifact-item"
          :class="{
            'artifact-item--active':
              viewingType === item.artifact_type && viewingNativeKind === (item.native_kind || ''),
          }"
          @click="viewContent(item.artifact_type, item.native_kind)"
        >
          <div class="artifact-item-main">
            <t-icon
              :name="
                item.artifact_type === ARTIFACT_TYPE_MARKDOWN
                  ? 'file-code'
                  : item.artifact_type === 'image_manifest'
                    ? 'file-image'
                    : 'file-setting'
              "
              size="14px"
              class="artifact-type-icon"
            />
            <span class="artifact-name">{{ artifactTypeLabel(item.artifact_type) }}</span>
            <span v-if="item.native_kind" class="artifact-native-kind">{{ item.native_kind }}</span>
          </div>
          <div class="artifact-item-meta">
            <span class="artifact-meta-tag">{{ item.format }}</span>
            <span class="artifact-meta-tag">{{ formatArtifactSize(item.size) }}</span>
            <t-button
              theme="default"
              variant="text"
              size="small"
              class="artifact-download-btn"
              @click.stop="handleDownload(item)"
            >
              <template #icon>
                <t-icon name="download" size="14px" />
              </template>
              {{ $t('knowledgeBase.artifactDownload') }}
            </t-button>
          </div>
        </div>
      </div>

      <div v-if="viewingContent || contentLoading || contentOversized" class="artifact-preview">
        <div v-if="contentLoading" class="artifact-loading">
          <t-loading size="small" />
        </div>
        <div v-else-if="contentError" class="artifact-error">
          <t-icon name="error-circle" size="14px" />
          <span>{{ contentError }}</span>
        </div>
        <div v-else-if="contentOversized" class="artifact-oversized">
          <t-icon name="error-circle" size="14px" />
          <span>{{ $t('knowledgeBase.artifactTooLarge') }}</span>
        </div>
        <div v-else-if="viewingContent" class="artifact-preview-content" ref="previewRoot">
          <pre v-if="viewingType !== ARTIFACT_TYPE_MARKDOWN" class="artifact-raw">{{ viewingContent }}</pre>
          <div v-else class="md-content" v-html="processedMarkdown"></div>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped lang="less">
@import './css/markdown.less';

.artifact-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.artifact-toolbar {
  display: flex;
  align-items: center;
  min-height: 24px;
}

.artifact-loading {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 0;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
}

.artifact-error,
.artifact-oversized {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 0;
  color: var(--td-error-color);
  font-size: 13px;
}

.artifact-empty {
  padding: 12px 0;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
}

.artifact-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.artifact-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 10px;
  border-radius: 4px;
  background: var(--td-bg-color-container-hover);
  cursor: pointer;
  transition: background-color 0.15s ease;

  &:hover {
    background: var(--td-bg-color-container-active);
  }

  &--active {
    background: var(--td-brand-color-light);
    border: 1px solid var(--td-brand-color);
  }
}

.artifact-item-main {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.artifact-type-icon {
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
}

.artifact-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.artifact-native-kind {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 120px;
}

.artifact-item-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.artifact-meta-tag {
  font-size: 11px;
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-component);
  padding: 1px 6px;
  border-radius: 3px;
  white-space: nowrap;
}

.artifact-download-btn {
  height: 24px;
  min-width: auto;
  padding: 0 8px;
  font-size: 12px;
}

.artifact-preview {
  margin-top: 4px;
  border-radius: 4px;
  border: 1px solid var(--td-component-border);
  overflow: hidden;
}

.artifact-preview-content {
  padding: 12px;
  max-height: 400px;
  overflow: auto;
}

.artifact-raw {
  margin: 0;
  font-size: 12px;
  font-family: 'Menlo', 'Consolas', 'Monaco', monospace;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--td-text-color-primary);
}
</style>
