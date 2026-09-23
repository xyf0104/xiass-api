import { describe, expect, it, vi } from 'vitest'

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn()
}))

import {
  allModels,
  buildModelMappingObject,
  fetchAntigravityDefaultMappings,
  getDefaultSelectedModelsByPlatform,
  getModelsByPlatform,
  getPresetMappingsByPlatform,
  normalizeAntigravityMappingsForDisplay,
  normalizeAntigravityModelsForDisplay,
  splitModelMappingObject,
} from '../useModelWhitelist'
import { getAntigravityDefaultModelMapping } from '@/api/admin/accounts'

const getAntigravityDefaultModelMappingMock = vi.mocked(getAntigravityDefaultModelMapping)

describe('useModelWhitelist', () => {
  it('openai 模型列表包含 GPT-5.4 官方快照', () => {
    const models = getModelsByPlatform('openai')

    expect(models).toContain('gpt-5.4')
    expect(models).toContain('gpt-5.4-mini')
    expect(models).toContain('gpt-5.4-2026-03-05')
    expect(models).toContain('codex-auto-review')
    expect(models).toContain('gpt-5.6')
    expect(models).toContain('gpt-5.6-luna')
    expect(models).toContain('gpt-6')
    expect(models).toContain('gpt-6-sol')
    expect(models).toContain('gpt-6-astra')
    expect(models).toContain('gpt-6-luna')
    expect(models).toContain('gpt-image-2.5-flare')
    expect(models).toContain('gpt-image-2.5-sunburst')

	const presets = getPresetMappingsByPlatform('openai')
	expect(presets).toEqual(expect.arrayContaining([
		expect.objectContaining({ from: 'gpt-6', to: 'gpt-6' }),
		expect.objectContaining({ from: 'gpt-6-astra', to: 'gpt-6-astra' }),
		expect.objectContaining({ from: 'gpt-6-sol', to: 'gpt-6-sol' }),
		expect.objectContaining({ from: 'gpt-6-luna', to: 'gpt-6-luna' })
	]))
  })

  it('OpenAI OAuth 默认支持 Luna，上游 API Key 需同步或显式选择后才声明支持', () => {
    expect(getDefaultSelectedModelsByPlatform('openai', 'oauth')).toContain('gpt-5.6-luna')
    expect(getDefaultSelectedModelsByPlatform('openai', 'apikey')).not.toContain('gpt-5.6-luna')
    expect(getModelsByPlatform('openai')).toContain('gpt-5.6-luna')
    expect(getDefaultSelectedModelsByPlatform('anthropic', 'apikey')).toEqual(getModelsByPlatform('anthropic'))
  })

  it('openai 模型列表不再暴露已下线的 ChatGPT 登录 Codex 模型', () => {
    const models = getModelsByPlatform('openai')

    expect(models).not.toContain('gpt-5')
    expect(models).not.toContain('gpt-5.1')
    expect(models).not.toContain('gpt-5.1-codex')
    expect(models).not.toContain('gpt-5.1-codex-max')
    expect(models).not.toContain('gpt-5.1-codex-mini')
    expect(models).not.toContain('gpt-5.2-codex')
  })

  it('antigravity 模型列表只展示当前图片模型', () => {
    const models = getModelsByPlatform('antigravity')

    expect(models).toContain('gemini-3.1-flash-image')
    expect(models).not.toContain('gemini-2.5-flash-image')
    expect(models).not.toContain('gemini-3-pro-image')
  })

  it('Claude 模型列表包含新发布的 Claude 模型', () => {
    expect(getModelsByPlatform('claude')).toContain('claude-fable-5-1')
    expect(getModelsByPlatform('claude')).toContain('claude-fable-5')
    expect(getModelsByPlatform('claude')).toContain('claude-opus-4-8')
    expect(getModelsByPlatform('antigravity')).toContain('claude-fable-5-1')
    expect(getModelsByPlatform('antigravity')).toContain('claude-opus-4-6-thinking')
    expect(getModelsByPlatform('antigravity')).toContain('claude-sonnet-4-6')
    expect(getModelsByPlatform('antigravity')).not.toContain('claude-opus-4-8')
  })

  it('xAI 模型列表包含 Grok 4.5 官方模型和别名', () => {
    const models = getModelsByPlatform('grok')

    expect(models).toContain('grok-4.5')
    expect(models).toContain('grok-4.5-latest')
    expect(models).toContain('grok-build-latest')
  })

  it('combined 模式支持 Grok 4.5 官方别名映射', () => {
    const mapping = buildModelMappingObject(
      'combined',
      ['grok-4.5'],
      [
        { from: 'grok-latest', to: 'grok-4.5' },
        { from: 'grok-4.5-latest', to: 'grok-4.5' },
        { from: 'grok-build-latest', to: 'grok-4.5' }
      ]
    )

    expect(mapping).toEqual({
      'grok-4.5': 'grok-4.5',
      'grok-latest': 'grok-4.5',
      'grok-4.5-latest': 'grok-4.5',
      'grok-build-latest': 'grok-4.5'
    })
  })

  it('grok 模型列表包含 Composer 默认项和兼容别名', () => {
    const models = getModelsByPlatform('grok')

    expect(models).toContain('grok-composer-2.5-fast')
    expect(models).toContain('grok-composer')
    expect(models).toContain('composer-2.5')
  })

  it('gemini 模型列表包含原生生图模型', () => {
    const models = getModelsByPlatform('gemini')

    expect(models).toContain('gemini-2.5-flash-image')
    expect(models).toContain('gemini-3.1-flash-image')
    expect(models.indexOf('gemini-3.1-flash-image')).toBeLessThan(models.indexOf('gemini-2.0-flash'))
    expect(models.indexOf('gemini-2.5-flash-image')).toBeLessThan(models.indexOf('gemini-2.5-flash'))
  })

  it('antigravity 模型列表会把当前 Gemini 图片模型排在前面', () => {
    const models = getModelsByPlatform('antigravity')

    expect(models.indexOf('gemini-3.1-flash-image')).toBeLessThan(models.indexOf('gemini-2.5-flash'))
  })

  it('antigravity 模型列表只保留可调用的 Gemini 3.1 Pro 档位', () => {
    const models = getModelsByPlatform('antigravity')

    expect(models).toContain('gemini-3.1-pro-high')
    expect(models).toContain('gemini-3.1-pro-low')
    expect(models).not.toContain('gemini-3.1-pro')
  })

  it('Antigravity 对外只展示 Gemini 3.7 Flash 三档自映射', () => {
    const models = [
      'gemini-3.7-flash-high',
      'gemini-3.7-flash-medium',
      'gemini-3.7-flash-low'
    ]
    const whitelist = getModelsByPlatform('antigravity')
    const presets = getPresetMappingsByPlatform('antigravity')

    for (const model of models) {
      expect(whitelist).toContain(model)
      expect(presets).toEqual(
        expect.arrayContaining([
          expect.objectContaining({ from: model, to: model })
        ])
      )
    }
    expect(whitelist).not.toContain('gemini-3.7-flash')
    expect(whitelist).not.toContain('gemini-3.7-flash-tiered')
    expect(allModels.map((model) => model.value)).toEqual(expect.arrayContaining(models))
    expect(allModels.map((model) => model.value)).not.toContain('gemini-3.7-flash')
    expect(allModels.map((model) => model.value)).not.toContain('gemini-3.7-flash-tiered')
    expect(
      presets.some((preset) =>
        preset.from.includes('gemini-3.7-flash-tiered') ||
        preset.to.includes('gemini-3.7-flash-tiered')
      )
    ).toBe(false)

    expect(buildModelMappingObject('whitelist', models, [])).toEqual(
      Object.fromEntries(models.map((model) => [model, model]))
    )
  })

  it('Antigravity 对外只展示 Gemini 3.8 Flash 三档自映射', () => {
    const models = [
      'gemini-3.8-flash-high',
      'gemini-3.8-flash-medium',
      'gemini-3.8-flash-low'
    ]
    const whitelist = getModelsByPlatform('antigravity')
    const presets = getPresetMappingsByPlatform('antigravity')

    for (const model of models) {
      expect(whitelist).toContain(model)
      expect(presets).toEqual(expect.arrayContaining([
        expect.objectContaining({ from: model, to: model })
      ]))
    }
    expect(whitelist).not.toContain('gemini-3.8-flash')
    expect(whitelist).not.toContain('gemini-3.8-flash-tiered')
    expect(allModels.map((model) => model.value)).toEqual(expect.arrayContaining(models))
    expect(allModels.map((model) => model.value)).not.toContain('gemini-3.8-flash')
    expect(allModels.map((model) => model.value)).not.toContain('gemini-3.8-flash-tiered')
  })

  it('把 Antigravity 3.7 内部模型名规范化为不重复的三档外部模型', () => {
    expect(normalizeAntigravityModelsForDisplay([
      'gemini-2.5-flash',
      'gemini-3.7-flash-tiered',
      'gemini-3.7-flash-high',
      'gemini-3.7-flash'
    ])).toEqual([
      'gemini-2.5-flash',
      'gemini-3.7-flash-high',
      'gemini-3.7-flash-medium',
      'gemini-3.7-flash-low'
    ])
  })

  it('把 Antigravity 3.8 内部模型名规范化为不重复的三档外部模型', () => {
    expect(normalizeAntigravityModelsForDisplay([
      'gemini-2.5-flash',
      'gemini-3.8-flash-tiered',
      'gemini-3.8-flash-high',
      'gemini-3.8-flash'
    ])).toEqual([
      'gemini-2.5-flash',
      'gemini-3.8-flash-high',
      'gemini-3.8-flash-medium',
      'gemini-3.8-flash-low'
    ])
  })

  it('旧 Antigravity 3.7 映射只回显三档自映射', () => {
    expect(normalizeAntigravityMappingsForDisplay({
      'gemini-3.7-flash': 'gemini-3.7-flash-tiered',
      'gemini-3.7-flash-tiered': 'gemini-3.7-flash-tiered',
      'gemini-3.7-flash-high': 'gemini-3.7-flash-tiered',
      'gemini-2.5-flash': 'gemini-2.5-flash'
    })).toEqual([
      { from: 'gemini-3.7-flash-high', to: 'gemini-3.7-flash-high' },
      { from: 'gemini-3.7-flash-medium', to: 'gemini-3.7-flash-medium' },
      { from: 'gemini-3.7-flash-low', to: 'gemini-3.7-flash-low' },
      { from: 'gemini-2.5-flash', to: 'gemini-2.5-flash' }
    ])
  })

  it('旧 Antigravity 3.8 映射只回显三档自映射', () => {
    expect(normalizeAntigravityMappingsForDisplay({
      'gemini-3.8-flash': 'gemini-3.8-flash-tiered',
      'gemini-3.8-flash-tiered': 'gemini-3.8-flash-tiered',
      'gemini-3.8-flash-high': 'gemini-3.8-flash-tiered'
    })).toEqual([
      { from: 'gemini-3.8-flash-high', to: 'gemini-3.8-flash-high' },
      { from: 'gemini-3.8-flash-medium', to: 'gemini-3.8-flash-medium' },
      { from: 'gemini-3.8-flash-low', to: 'gemini-3.8-flash-low' }
    ])
  })

  it('默认映射 API 返回内部模型名时不会泄漏到创建账号界面', async () => {
    getAntigravityDefaultModelMappingMock.mockResolvedValue({
      'gemini-3.7-flash': 'gemini-3.7-flash-tiered',
      'gemini-3.7-flash-high': 'gemini-3.7-flash-tiered',
      'gemini-3.7-flash-medium': 'gemini-3.7-flash-tiered',
      'gemini-3.7-flash-low': 'gemini-3.7-flash-tiered'
    })

    const mappings = await fetchAntigravityDefaultMappings()

    expect(mappings).toEqual([
      { from: 'gemini-3.7-flash-high', to: 'gemini-3.7-flash-high' },
      { from: 'gemini-3.7-flash-medium', to: 'gemini-3.7-flash-medium' },
      { from: 'gemini-3.7-flash-low', to: 'gemini-3.7-flash-low' }
    ])
    expect(JSON.stringify(mappings)).not.toContain('gemini-3.7-flash-tiered')
    expect(JSON.stringify(mappings)).not.toContain('"gemini-3.7-flash"')
  })

  it('whitelist 模式会忽略通配符条目', () => {
    const mapping = buildModelMappingObject('whitelist', ['claude-*', 'gemini-3.1-flash-image'], [])
    expect(mapping).toEqual({
      'gemini-3.1-flash-image': 'gemini-3.1-flash-image'
    })
  })

  it('whitelist 模式会保留 GPT-5.4 官方快照的精确映射', () => {
    const mapping = buildModelMappingObject('whitelist', ['gpt-5.4-2026-03-05'], [])

    expect(mapping).toEqual({
      'gpt-5.4-2026-03-05': 'gpt-5.4-2026-03-05'
    })
  })

  it('whitelist keeps GPT-5.4 mini exact mappings', () => {
    const mapping = buildModelMappingObject('whitelist', ['gpt-5.4-mini'], [])

    expect(mapping).toEqual({
      'gpt-5.4-mini': 'gpt-5.4-mini'
    })
  })

  it('combined 模式会同时保留白名单身份映射和模型映射', () => {
    const mapping = buildModelMappingObject(
      'combined',
      ['gpt-5.4', 'claude-*'],
      [
        { from: 'gpt-latest', to: 'gpt-5.4' },
        { from: 'gpt-5.4', to: 'gpt-5.4-mini' }
      ]
    )

    expect(mapping).toEqual({
      'gpt-5.4': 'gpt-5.4-mini',
      'gpt-latest': 'gpt-5.4'
    })
  })

  it('splitModelMappingObject 会把身份映射还原成白名单，其余保留为映射', () => {
    const parsed = splitModelMappingObject({
      'gpt-5.4': 'gpt-5.4',
      'gpt-latest': 'gpt-5.4',
      ' ': 'gpt-empty',
      broken: 123
    })

    expect(parsed).toEqual({
      allowedModels: ['gpt-5.4'],
      modelMappings: [{ from: 'gpt-latest', to: 'gpt-5.4' }]
    })
  })
})
