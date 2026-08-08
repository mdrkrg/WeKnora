// 知识文档 Markdown 渲染管线：与 doc-content.vue 的 processMarkdown 保持
// 同一处理链（frontmatter 剥离、HTML 实体还原、表格归一、数学定界符、
// marked(breaks/gfm/katex) + hljs 代码高亮、DOMPurify 净化），供产物预览
// 等场景复用，保证与全文视图渲染一致。

import { marked } from 'marked'
import markedKatex from 'marked-katex-extension'
import hljs from 'highlight.js'

import { preprocessMathDelimiters } from './chatMarkdownRenderer'
import { normalizeSpuriousTablePrefixes } from './markdownTableNormalize'
import { safeMarkdownToHTML, sanitizeHTML } from './security'

let markedConfigured = false

/** 幂等地配置全局 marked（breaks/gfm/katex）。 */
export function configureKnowledgeMarkdownRenderer(): void {
  if (markedConfigured) return
  marked.use({
    breaks: true, // 启用单行换行转 <br>
    gfm: true, // 启用 GitHub Flavored Markdown
  })
  marked.use(markedKatex({ throwOnError: false, nonStandard: true }))
  markedConfigured = true
}

const renderer = new marked.Renderer()

// Mermaid 图表：输出占位 div，由调用方在挂载后执行 mermaid.run()
// 代码块：hljs 高亮并套用与 doc-content 一致的代码块外壳
renderer.code = function ({ text, lang }: { text: string; lang?: string }) {
  if (!text || typeof text !== 'string') {
    text = ''
  }
  if (lang === 'mermaid') {
    return `<div class="mermaid">${text}</div>`
  }

  let detectedLang = lang
  let highlighted = ''
  if (lang && hljs.getLanguage(lang)) {
    try {
      highlighted = hljs.highlight(text, { language: lang }).value
    } catch {
      highlighted = hljs.highlightAuto(text).value
      detectedLang = hljs.highlightAuto(text).language || lang
    }
  } else {
    const auto = hljs.highlightAuto(text)
    highlighted = auto.value
    detectedLang = auto.language || lang
  }
  const displayLang = detectedLang || 'Code'
  return `
    <div class="code-block-wrapper">
      <div class="code-block-header">
        <span class="code-block-lang">${displayLang}</span>
      </div>
      <pre class="code-block-pre"><code class="hljs language-${detectedLang || ''}">${highlighted}</code></pre>
    </div>
  `
}

/**
 * 将 Markdown 文本渲染为已净化的 HTML。
 * 渲染结果需通过 v-html 使用；图片等资源引用由调用方按场景处理。
 */
export function renderKnowledgeMarkdown(markdownText: string): string {
  if (!markdownText || typeof markdownText !== 'string') return ''
  configureKnowledgeMarkdownRenderer()

  // 去除 Markdown 头部的 YAML Frontmatter（例如 --- title: xxx ---）
  let processedText = markdownText.replace(/^\s*---\r?\n[\s\S]*?\r?\n---\r?\n/, '')

  // 先还原原始文本中的 HTML 实体，让它们作为普通字符参与渲染
  processedText = processedText
    .replace(/&#39;/g, "'")
    .replace(/&#x27;/gi, "'")
    .replace(/&apos;/g, "'")
    .replace(/&#34;/g, '"')
    .replace(/&#x22;/gi, '"')
    .replace(/&quot;/g, '"')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&')

  // 处理被 <p> 包裹的表格行，转换为正常的表格行，并在前后补空行
  processedText = processedText.replace(/<p>\s*(\|[\s\S]*?\|)\s*<\/p>/gi, '\n$1\n')

  // MarkItDown 常在表格前插入空行 + 分隔行，渲染会出现多余空行
  processedText = normalizeSpuriousTablePrefixes(processedText)

  // 先预处理数学定界符，再做安全预处理
  const mathSafeText = preprocessMathDelimiters(processedText)
  const safeMarkdown = safeMarkdownToHTML(mathSafeText)

  // 使用标记渲染
  marked.use({ renderer })
  let html = marked.parse(safeMarkdown) as string

  // 还原被转义的 <br>
  html = html.replace(/&lt;br\s*\/?&gt;/gi, '<br>')

  // 最终安全清理
  return sanitizeHTML(html)
}
