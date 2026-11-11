import { describe, it, expect } from 'vitest'
import { parseChangelog, segments } from './changelogPreview'

describe('parseChangelog', () => {
  it('parses headings, list items, paragraphs and bare URLs', () => {
    expect(parseChangelog('## [18.6-6] - 2026-11-11\n\n### Changed\n- a (https://x.y/z)\n<script>')).toEqual([
      { kind: 'h2', text: '[18.6-6] - 2026-11-11', links: [] },
      { kind: 'h3', text: 'Changed', links: [] },
      { kind: 'li', text: 'a (https://x.y/z)', links: [{ text: 'https://x.y/z', href: 'https://x.y/z' }] },
      { kind: 'p', text: '<script>', links: [] },
    ])
  })

  it('accepts * bullets and drops trailing punctuation from links', () => {
    const [n] = parseChangelog('* see http://a.b/c.')
    expect(n.kind).toBe('li')
    expect(n.links).toEqual([{ text: 'http://a.b/c', href: 'http://a.b/c' }])
  })

  it('never makes a link out of javascript: URLs', () => {
    const [n] = parseChangelog('click javascript:alert(1) now')
    expect(n.kind).toBe('p')
    expect(n.links).toEqual([])
  })
})

describe('segments', () => {
  it('splits text around links', () => {
    const [n] = parseChangelog('- a (https://x.y/z) b')
    expect(segments(n)).toEqual([
      { text: 'a (' },
      { text: 'https://x.y/z', href: 'https://x.y/z' },
      { text: ') b' },
    ])
  })
})
