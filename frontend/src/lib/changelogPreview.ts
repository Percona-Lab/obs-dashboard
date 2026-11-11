// Minimal, safe changelog renderer: produces plain nodes, never HTML.
export type PreviewNode = { kind: 'h2' | 'h3' | 'li' | 'p'; text: string; links: { text: string; href: string }[] }
export type Segment = { text: string; href?: string }

const URL_RE = /https?:\/\/[^\s<>"'`)\]]+/g

function findLinks(text: string): { text: string; href: string }[] {
  return (text.match(URL_RE) ?? []).map((u) => {
    const t = u.replace(/[.,;:!?]+$/, '')
    return { text: t, href: t }
  })
}

export function parseChangelog(md: string): PreviewNode[] {
  const out: PreviewNode[] = []
  for (const raw of md.split('\n')) {
    const line = raw.trim()
    if (!line) continue
    let kind: PreviewNode['kind'] = 'p'
    let text = line
    if (line.startsWith('### ')) { kind = 'h3'; text = line.slice(4).trim() }
    else if (line.startsWith('## ')) { kind = 'h2'; text = line.slice(3).trim() }
    else if (line.startsWith('- ') || line.startsWith('* ')) { kind = 'li'; text = line.slice(2).trim() }
    out.push({ kind, text, links: findLinks(text) })
  }
  return out
}

// segments splits a node's text into plain and link parts, in order.
export function segments(n: PreviewNode): Segment[] {
  const segs: Segment[] = []
  let rest = n.text
  for (const l of n.links) {
    const i = rest.indexOf(l.href)
    if (i < 0) continue
    if (i > 0) segs.push({ text: rest.slice(0, i) })
    segs.push({ text: l.text, href: l.href })
    rest = rest.slice(i + l.href.length)
  }
  if (rest) segs.push({ text: rest })
  return segs
}
