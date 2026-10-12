// Repository setup snippets shown in the Artifacts packages sub-tab.
//
// Commands that write system files or refresh package metadata run under
// sudo when opts.sudo is set (the default in the UI, PG-2745); user-side
// pipes such as curl and gpg --dearmor never do. The toggle exists because
// root shells and minimal containers often have no sudo binary at all.

export interface RepoSetupArgs {
  /** Full OBS project name, e.g. "isv:percona:ppg:staging:18". */
  obsProjectName: string
  /** Repository download base URL, trailing slash included. */
  baseUrl: string
  /** OBS repository name, e.g. "UBI_9" or "xUbuntu_24.04". */
  repoObs: string
  repoType: 'rpm' | 'deb'
}

export interface RepoSetupOptions {
  sudo: boolean
}

export function repoSetupSnippet(args: RepoSetupArgs, opts: RepoSetupOptions): string {
  const { obsProjectName, baseUrl, repoObs, repoType } = args
  const su = opts.sudo ? 'sudo ' : ''
  // apt ignores files in sources.list.d whose names contain characters
  // outside [A-Za-z0-9_.-], so the colon-separated project name cannot be
  // used as-is there; yum has the same filename for consistency.
  const projectId = obsProjectName.split(':').join('_')

  if (repoObs.startsWith('openSUSE')) {
    return `${su}zypper addrepo \\
  ${baseUrl} \\
  ${obsProjectName}
${su}zypper --gpg-auto-import-keys refresh`
  }

  if (repoType === 'rpm') {
    return `${su}rpm --import ${baseUrl}repodata/repomd.xml.key
${su}tee /etc/yum.repos.d/${projectId}.repo << 'EOF'
[${obsProjectName}]
name=${obsProjectName} - ${repoObs}
baseurl=${baseUrl}
enabled=1
gpgcheck=0
EOF`
  }

  return `echo 'deb ${baseUrl} /' \\
  | ${su}tee /etc/apt/sources.list.d/${projectId}.list
curl -fsSL ${baseUrl}Release.key \\
  | gpg --dearmor | ${su}tee /etc/apt/trusted.gpg.d/${projectId}.gpg > /dev/null
${su}apt update`
}
