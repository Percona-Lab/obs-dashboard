// Detection helpers for tarball build artifacts. A tarball is a build target
// in a `…:tarballs` subproject whose repo is an OpenSSL-versioned repo
// (ssl1.1 / ssl3 / ssl3.5 / …). RockyLinux-built packages inside a :tarballs
// subproject (e.g. percona-psql) are NOT tarballs and stay in the Packages view.

export function isTarballRepo(repo: string): boolean {
  return /^ssl/i.test(repo)
}

export function isTarballTarget(project: string, repo: string): boolean {
  return project.endsWith(':tarballs') && isTarballRepo(repo)
}

// tarballRepoOrder sorts ssl repos naturally: ssl1.1 < ssl3 < ssl3.5.
export function tarballRepoOrder(a: string, b: string): number {
  return a.localeCompare(b, undefined, { numeric: true, sensitivity: 'base' })
}
