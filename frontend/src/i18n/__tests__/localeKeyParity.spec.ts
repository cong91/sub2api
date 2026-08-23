import { describe, expect, it } from 'vitest'

import en from '../locales/en'
import vi from '../locales/vi'
import zh from '../locales/zh'

function collectLeafPaths(node: unknown, path = '', out = new Set<string>()): Set<string> {
  if (node && typeof node === 'object' && !Array.isArray(node)) {
    for (const [key, value] of Object.entries(node as Record<string, unknown>)) {
      collectLeafPaths(value, path ? `${path}.${key}` : key, out)
    }
    return out
  }

  out.add(path)
  return out
}

describe('locale key parity', () => {
  it('Vietnamese contains every Chinese message key', () => {
    const zhKeys = collectLeafPaths(zh)
    const viKeys = collectLeafPaths(vi)
    const missing = [...zhKeys].filter((key) => !viKeys.has(key))

    expect(missing).toEqual([])
  })

  it('Vietnamese preserves English message placeholders', () => {
    const enMessages = collectMessages(en)
    const viMessages = collectMessages(vi)
    const mismatches = Object.keys(enMessages).filter(
      (key) => JSON.stringify(placeholders(enMessages[key])) !== JSON.stringify(placeholders(viMessages[key]))
    )

    expect(mismatches).toEqual([])
  })
})

function collectMessages(node: unknown, path = '', out: Record<string, string> = {}): Record<string, string> {
  if (typeof node === 'string') {
    out[path] = node
    return out
  }

  if (node && typeof node === 'object' && !Array.isArray(node)) {
    for (const [key, value] of Object.entries(node as Record<string, unknown>)) {
      collectMessages(value, path ? `${path}.${key}` : key, out)
    }
  }

  return out
}

function placeholders(message: string | undefined): string[] {
  return typeof message === 'string' ? [...message.matchAll(/\{[^{}]+\}/g)].map((match) => match[0]).sort() : []
}
