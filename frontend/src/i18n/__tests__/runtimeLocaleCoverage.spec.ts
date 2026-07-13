import { readFileSync, readdirSync } from 'node:fs'
import { dirname, extname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import en from '../locales/en/index'
import zh from '../locales/zh/index'

const srcRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../..')

function hasLocaleKey(messages: Record<string, unknown>, key: string): boolean {
  let current: unknown = messages
  for (const segment of key.split('.')) {
    if (!current || typeof current !== 'object' || !(segment in current)) return false
    current = (current as Record<string, unknown>)[segment]
  }
  return current !== undefined
}

function sourceFiles(directory: string): string[] {
  const files: string[] = []
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    if (entry.name === '__tests__' || entry.name === 'i18n') continue
    const path = join(directory, entry.name)
    if (entry.isDirectory()) {
      files.push(...sourceFiles(path))
      continue
    }
    if (['.ts', '.tsx', '.vue'].includes(extname(entry.name)) && !entry.name.includes('.spec.')) {
      files.push(path)
    }
  }
  return files
}

function staticallyReferencedLocaleKeys(): string[] {
  const keys = new Set<string>()
  const pattern = /(?<![A-Za-z0-9_$])(?:\$t|t)\(\s*["']([A-Za-z0-9_]+(?:\.[A-Za-z0-9_-]+)+)["']/g

  for (const path of sourceFiles(srcRoot)) {
    const source = readFileSync(path, 'utf8')
    if (!source.includes('useI18n') && !source.includes('$t(')) continue
    for (const match of source.matchAll(pattern)) keys.add(match[1])
  }
  return [...keys].sort()
}

describe('runtime locale contract', () => {
  it('defines every statically referenced full key in both locales', () => {
    const missing = staticallyReferencedLocaleKeys().flatMap((key) => {
      const locales = [
        !hasLocaleKey(zh, key) && 'zh',
        !hasLocaleKey(en, key) && 'en',
      ].filter(Boolean)
      return locales.length ? [`${key}: ${locales.join(', ')}`] : []
    })

    expect(missing).toEqual([])
  })

  it('preserves XIASS navigation, pricing, and newly added page titles', () => {
    expect(zh.nav.availableChannels).toBe('模型价格')
    expect(zh.nav.buySubscription).toBe('充值')
    expect(zh.admin.dashboard.groupPricing).toBe('分组定价')
    expect(zh.usage.tabs.ranking).toBe('用户排行')
    expect(zh.setup.title).toBe('XIASS API 安装向导')
    expect(en.setup.title).toBe('XIASS API Setup')
  })
})
