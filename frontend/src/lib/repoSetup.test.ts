import { describe, it, expect } from 'vitest'
import { repoSetupSnippet } from './repoSetup'

// Shapes mirror what PackagesSubTab derives for isv:percona:ppg:staging:18
// on the openSUSE instance.
const PROJECT = 'isv:percona:ppg:staging:18'
const BASE = 'https://download.opensuse.org/repositories/isv:/percona:/ppg:/staging:/18/'

describe('repoSetupSnippet', () => {
  it('builds the zypper snippet with sudo on the system commands', () => {
    expect(repoSetupSnippet(
      { obsProjectName: PROJECT, baseUrl: BASE + 'openSUSE_Tumbleweed/', repoObs: 'openSUSE_Tumbleweed', repoType: 'rpm' },
      { sudo: true },
    )).toBe(`sudo zypper addrepo \\
  ${BASE}openSUSE_Tumbleweed/ \\
  ${PROJECT}
sudo zypper --gpg-auto-import-keys refresh`)
  })

  it('builds the zypper snippet without sudo', () => {
    expect(repoSetupSnippet(
      { obsProjectName: PROJECT, baseUrl: BASE + 'openSUSE_Tumbleweed/', repoObs: 'openSUSE_Tumbleweed', repoType: 'rpm' },
      { sudo: false },
    )).toBe(`zypper addrepo \\
  ${BASE}openSUSE_Tumbleweed/ \\
  ${PROJECT}
zypper --gpg-auto-import-keys refresh`)
  })

  it('builds the yum repo file with sudo on rpm --import and tee', () => {
    expect(repoSetupSnippet(
      { obsProjectName: PROJECT, baseUrl: BASE + 'UBI_9/', repoObs: 'UBI_9', repoType: 'rpm' },
      { sudo: true },
    )).toBe(`sudo rpm --import ${BASE}UBI_9/repodata/repomd.xml.key
sudo tee /etc/yum.repos.d/isv_percona_ppg_staging_18.repo << 'EOF'
[${PROJECT}]
name=${PROJECT} - UBI_9
baseurl=${BASE}UBI_9/
enabled=1
gpgcheck=0
EOF`)
  })

  it('builds the yum repo file without sudo', () => {
    expect(repoSetupSnippet(
      { obsProjectName: PROJECT, baseUrl: BASE + 'UBI_9/', repoObs: 'UBI_9', repoType: 'rpm' },
      { sudo: false },
    )).toBe(`rpm --import ${BASE}UBI_9/repodata/repomd.xml.key
tee /etc/yum.repos.d/isv_percona_ppg_staging_18.repo << 'EOF'
[${PROJECT}]
name=${PROJECT} - UBI_9
baseurl=${BASE}UBI_9/
enabled=1
gpgcheck=0
EOF`)
  })

  it('builds the apt snippet with sudo only on tee and apt, and an apt-safe list filename', () => {
    expect(repoSetupSnippet(
      { obsProjectName: PROJECT, baseUrl: BASE + 'xUbuntu_24.04/', repoObs: 'xUbuntu_24.04', repoType: 'deb' },
      { sudo: true },
    )).toBe(`echo 'deb ${BASE}xUbuntu_24.04/ /' \\
  | sudo tee /etc/apt/sources.list.d/isv_percona_ppg_staging_18.list
curl -fsSL ${BASE}xUbuntu_24.04/Release.key \\
  | gpg --dearmor | sudo tee /etc/apt/trusted.gpg.d/isv_percona_ppg_staging_18.gpg > /dev/null
sudo apt update`)
  })

  it('builds the apt snippet without sudo, keeping the apt-safe list filename', () => {
    expect(repoSetupSnippet(
      { obsProjectName: PROJECT, baseUrl: BASE + 'xUbuntu_24.04/', repoObs: 'xUbuntu_24.04', repoType: 'deb' },
      { sudo: false },
    )).toBe(`echo 'deb ${BASE}xUbuntu_24.04/ /' \\
  | tee /etc/apt/sources.list.d/isv_percona_ppg_staging_18.list
curl -fsSL ${BASE}xUbuntu_24.04/Release.key \\
  | gpg --dearmor | tee /etc/apt/trusted.gpg.d/isv_percona_ppg_staging_18.gpg > /dev/null
apt update`)
  })
})
