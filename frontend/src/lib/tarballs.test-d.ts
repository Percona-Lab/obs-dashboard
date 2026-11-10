import { isTarballRepo, isTarballTarget, tarballRepoOrder } from './tarballs'

// Pins the helper signatures.
const a: boolean = isTarballRepo('ssl3')
const b: boolean = isTarballTarget('ppg:staging:18:tarballs', 'ssl3')
const c: number = tarballRepoOrder('ssl1.1', 'ssl3')
void a
void b
void c
