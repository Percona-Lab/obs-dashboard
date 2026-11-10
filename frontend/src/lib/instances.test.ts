import { describe, it, expect } from 'vitest'
import { binaryUrl } from './instances'

// PG-2860: OBS publishes files under the arch in the filename, not the build arch.
describe('binaryUrl', () => {
  const base = 'https://download.opensuse.org/repositories/isv:/percona:/ppg:/staging:/18/'

  it('uses the Debian arch from the .deb name', () => {
    expect(binaryUrl(`${base}Ubuntu_26.04/`, 'x86_64', 'percona-pg-stat-monitor18_2.4.1-1+1.1.resolute_amd64.deb'))
      .toBe(`${base}Ubuntu_26.04/amd64/percona-pg-stat-monitor18_2.4.1-1+1.1.resolute_amd64.deb`)
    expect(binaryUrl(`${base}Ubuntu_26.04/`, 'aarch64', 'etcd_3.5.35-1+1.1.resolute_all.deb'))
      .toBe(`${base}Ubuntu_26.04/all/etcd_3.5.35-1+1.1.resolute_all.deb`)
  })

  it('uses the RPM arch from the .rpm name', () => {
    expect(binaryUrl(`${base}RockyLinux_9/`, 'x86_64', 'gdal313-javadoc-3.13.3-3.10.el9.noarch.rpm'))
      .toBe(`${base}RockyLinux_9/noarch/gdal313-javadoc-3.13.3-3.10.el9.noarch.rpm`)
    expect(binaryUrl(`${base}RockyLinux_9/`, 'x86_64', 'percona-postgresql18-18.6.1-1.el9.x86_64.rpm'))
      .toBe(`${base}RockyLinux_9/x86_64/percona-postgresql18-18.6.1-1.el9.x86_64.rpm`)
  })

  it('falls back to the build arch, and to empty without a base', () => {
    expect(binaryUrl(base, 'x86_64', 'other.bin')).toBe(`${base}x86_64/other.bin`)
    expect(binaryUrl('', 'x86_64', 'a_1_amd64.deb')).toBe('')
  })
})
