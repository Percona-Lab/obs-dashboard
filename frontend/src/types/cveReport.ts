// Mirrors the JSON produced by backend/internal/cvereport exactly.
export type CveList = 'ours' | 'base_os' | 'unclassified'
export type CveKind = 'go_runtime' | 'bundled_library' | 'obs_package' | 'base_os' | 'unclassified'
export interface CveFix { pkg: string; installed: string; fixed: string }
export interface CveEntry { id: string; pkg: string; path?: string; severity: 'HIGH' | 'CRITICAL'; title: string; seen_key: string }
export interface CveImageRef { project: string; package: string; repo: string; arches: string[] }
export interface CveRow { key: string; name: string; kind: CveKind; vendor?: string; worst: 'HIGH' | 'CRITICAL'; cves: number; fixes: CveFix[]; entries: CveEntry[]; images: CveImageRef[] }
export interface CveStaging { ours: CveRow[]; base_os: CveRow[]; unclassified: CveRow[]; cves: number; images: number }
export interface ReleasedCve { id: string; pkg: string; installed: string; fixed: string; severity: 'HIGH' | 'CRITICAL'; title: string; status: 'fixed' | 'open' | 'no_staging'; staging_row_key?: string }
export interface ReleasedImage { project: string; package: string; repo: string; version: string; staging_project: string; staging_version?: string; total: number; fixed_in_staging: number; cves: ReleasedCve[] }
export interface CveReport { generated_at: string; staging: CveStaging; released: ReleasedImage[] }
export type CveView = 'staging' | 'released' | 'images'
