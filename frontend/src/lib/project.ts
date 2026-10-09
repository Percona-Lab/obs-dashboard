// Logical project names are root-free: "ppg:staging:17", "ppg:common",
// "common", "PR:pr-92:ppg:staging:17", "ppg:releases:17".

export function isPRProject(project: string): boolean {
  return project.split(':')[0] === 'PR'
}

function inTree(project: string, base: string): boolean {
  return project === base || project.startsWith(base + ':')
}

// Does project belong to the context with this prefix? A context covers its
// own subtree plus shared common trees: PR contexts add PR:<pr>:common;
// product (devel/staging) contexts add <product>:common and the global
// common tree. Release contexts are exact subtrees.
export function projectInContext(project: string, prefix: string): boolean {
  if (!prefix || inTree(project, prefix)) return true
  const parts = prefix.split(':')
  if (parts[0] === 'PR') return inTree(project, `${parts.slice(0, 2).join(':')}:common`)
  if (parts[1] === 'releases') return false
  return inTree(project, `${parts[0]}:common`) || inTree(project, 'common')
}

// Is project strictly below prefix (prefix:<more>)? The prefix root itself
// and anything outside it (shared common trees) are not under the prefix,
// and the version filters always show those.
export function isUnderPrefix(project: string, prefix: string): boolean {
  return project.startsWith(prefix + ':')
}
