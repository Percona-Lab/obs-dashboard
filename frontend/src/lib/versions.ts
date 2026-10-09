// Version-key model for the <version>[:<subproject>] selector: subprojects
// of a version (extras, tde, tools, …) are "version extensions" with their
// own selector entries, while absorbed subprojects (containers, tarballs)
// fold into the plain version entry. The version segment is usually a
// major number but may be a literal such as "common" (the cross-version
// projects under ppg:staging:common). Shared by the board (usePackages),
// the artifacts panel, and the events filter so all three scope
// identically.

/** Split "17:extras" into ["17", "extras"]; "17" into ["17", undefined]. */
export function splitVersionKey(key: string): [string, string | undefined] {
  const idx = key.indexOf(':')
  return idx < 0 ? [key, undefined] : [key.slice(0, idx), key.slice(idx + 1)]
}

const isNumeric = (s: string): boolean => /^\d+$/.test(s)

/** Numeric versions first (descending), then non-numeric alphabetically. */
function compareVersions(a: string, b: string): number {
  const an = isNumeric(a)
  const bn = isNumeric(b)
  if (an && bn) return parseInt(b, 10) - parseInt(a, 10)
  if (an !== bn) return an ? -1 : 1
  return a.localeCompare(b)
}

/** Derive selector keys from project paths under prefix. Projects outside
 *  prefix (shared common trees) contribute nothing. absorbed lists the
 *  subprojects folded into the plain version entry (undefined = catch-all
 *  context: numeric plain keys only). Order: numeric versions descending, then
 *  non-numeric alphabetically; within a version the plain key first, then
 *  extensions alphabetical. */
export function deriveVersionKeys(
  projects: Iterable<string>,
  prefix: string,
  absorbed: string[] | undefined,
): string[] {
  const depth = prefix.split(':').length
  const plain = new Set<string>()
  const extensions = new Set<string>()
  for (const project of projects) {
    if (!project.startsWith(prefix + ':')) continue
    const parts = project.split(':')
    const ver = parts[depth]
    if (!ver) continue
    if (absorbed === undefined && !isNumeric(ver)) continue
    const sub = parts[depth + 1]
    if (!sub || absorbed === undefined || absorbed.includes(sub)) {
      plain.add(ver)
    } else {
      extensions.add(`${ver}:${sub}`)
    }
  }
  const versions = new Set<string>(plain)
  for (const ext of extensions) versions.add(splitVersionKey(ext)[0])
  const keys: string[] = []
  for (const ver of [...versions].sort(compareVersions)) {
    if (plain.has(ver)) keys.push(ver)
    keys.push(...[...extensions].filter(e => e.startsWith(ver + ':')).sort())
  }
  return keys
}

/** Does project participate in version scoping under prefix? False for
 *  projects outside the prefix (shared common trees, the prefix root) and,
 *  in catch-all contexts (absorbed undefined), for projects whose version
 *  segment is not numeric — those are always shown. */
export function scopedByVersion(project: string, prefix: string, absorbed: string[] | undefined): boolean {
  if (!project.startsWith(prefix + ':')) return false
  if (absorbed !== undefined) return true
  const ver = project.split(':')[prefix.split(':').length]
  return ver !== undefined && isNumeric(ver)
}

/** Does a project belong under prefix at the selected version key?
 *  Plain key "17": the version root plus absorbed subprojects only (when
 *  absorbed is defined; catch-all contexts match the whole subtree).
 *  Extension key "17:extras": the prefix:17:extras subtree (catch-all,
 *  containers beneath included). */
export function matchesVersionKey(
  project: string,
  prefix: string,
  key: string,
  absorbed: string[] | undefined,
): boolean {
  const [ver, sub] = splitVersionKey(key)
  const base = sub ? `${prefix}:${ver}:${sub}` : `${prefix}:${ver}`
  if (project === base) return true
  if (!project.startsWith(base + ':')) return false
  if (sub || absorbed === undefined) return true
  const first = project.slice(base.length + 1).split(':')[0]
  return absorbed.includes(first)
}

/** The base OBS project a key stands for: "ppg:staging:16:tde" for
 *  "16:tde", "ppg:staging:common" for "common", prefix for the empty key. */
export function versionKeyProject(prefix: string, key: string): string {
  if (!key) return prefix
  const [ver, sub] = splitVersionKey(key)
  return sub ? `${prefix}:${ver}:${sub}` : `${prefix}:${ver}`
}

/** Distinct version tokens of a key list, in list order (the selector's
 *  first row). */
export function selectorVersions(keys: string[]): string[] {
  const out: string[] = []
  for (const key of keys) {
    const ver = splitVersionKey(key)[0]
    if (!out.includes(ver)) out.push(ver)
  }
  return out
}

/** The key selected when a version chip is clicked: the plain key when it
 *  exists, otherwise the version's first extension (keys are ordered plain
 *  first). undefined when the version has no keys. */
export function keyForVersion(keys: string[], version: string): string | undefined {
  return keys.find(k => splitVersionKey(k)[0] === version)
}
