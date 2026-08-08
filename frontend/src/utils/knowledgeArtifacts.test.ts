import assert from 'node:assert/strict'
import test from 'node:test'

import {
  artifactFileName,
  artifactTypeLabel,
  buildArtifactQuery,
  formatArtifactSize,
} from './knowledgeArtifacts.ts'

test('buildArtifactQuery omits empty params and returns empty string', () => {
  assert.equal(buildArtifactQuery(), '')
  assert.equal(buildArtifactQuery({}), '')
  assert.equal(buildArtifactQuery({ type: '' }), '')
})

test('buildArtifactQuery encodes present params in stable order', () => {
  assert.equal(
    buildArtifactQuery({ type: 'markdown', native_kind: 'md-content', resolve_images: true }),
    '?type=markdown&native_kind=md-content&resolve_images=true',
  )
})

test('buildArtifactQuery includes attempt only when truthy', () => {
  assert.equal(buildArtifactQuery({ attempt: 0 }), '')
  assert.equal(buildArtifactQuery({ attempt: 2 }), '?attempt=2')
})

test('formatArtifactSize renders B, KB, MB and GB', () => {
  assert.equal(formatArtifactSize(0), '0 B')
  assert.equal(formatArtifactSize(1023), '1023 B')
  assert.equal(formatArtifactSize(2048), '2.0 KB')
  assert.equal(formatArtifactSize(1048576), '1.0 MB')
  assert.equal(formatArtifactSize(1073741824), '1.0 GB')
})

test('artifactTypeLabel maps known types and falls back to raw value', () => {
  assert.equal(artifactTypeLabel('markdown'), 'Markdown')
  assert.equal(artifactTypeLabel('image_manifest'), 'Image Manifest')
  assert.equal(artifactTypeLabel('engine_native'), 'Engine Native')
  assert.equal(artifactTypeLabel('unknown_type'), 'unknown_type')
})

test('artifactFileName uses native_kind json naming when present', () => {
  assert.equal(
    artifactFileName({ artifact_type: 'engine_native', native_kind: 'mineru', format: 'json' }),
    'engine_native-mineru.json',
  )
})

test('artifactFileName falls back to format extension and bin default', () => {
  assert.equal(artifactFileName({ artifact_type: 'markdown', format: 'markdown' }), 'markdown.md')
  assert.equal(artifactFileName({ artifact_type: 'markdown', format: 'json' }), 'markdown.json')
  assert.equal(artifactFileName({ artifact_type: 'image_manifest' }), 'image_manifest.bin')
})
