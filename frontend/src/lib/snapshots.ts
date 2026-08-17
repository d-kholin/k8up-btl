import type { K8sObject } from '../api'

export type SnapSpec = { date?: string; id?: string; paths?: string[]; repository?: string }

export function snapSpec(s: K8sObject): SnapSpec {
  return (s.spec || {}) as SnapSpec
}

export function snapTime(s: K8sObject): number {
  const d = snapSpec(s).date || s.creationTimestamp
  return d ? new Date(d).getTime() : 0
}

export function workloadFromPaths(paths: string[]): string {
  if (!paths.length) return '(unknown)'
  return paths
    .map((p) => p.replace(/\/+$/, '').split('/').filter(Boolean).pop() || p)
    .join(', ')
}

export function guessPvc(s: K8sObject): string {
  return sourcePvcCandidates(s)[0] || ''
}

// sourcePvcCandidates mirrors the backend's SourcePVCCandidates: the PVC names
// a snapshot was taken from, derived from spec.paths (K8up mounts each PVC at
// /data/<pvcName>). Restores are only allowed onto one of these.
// isSqlDump: application-level dump snapshot (K8up backupcommand/stdin backup)
// — restored by piping into the DB client, not onto a PVC.
export function isSqlDump(s: K8sObject): boolean {
  const paths = snapSpec(s).paths || []
  return paths.some((p) => p.endsWith('.sql'))
}

export function sourcePvcCandidates(s: K8sObject): string[] {
  const paths = snapSpec(s).paths || []
  const out: string[] = []
  for (const p of paths) {
    const base = p.replace(/\/+$/, '').split('/').filter(Boolean).pop() || ''
    if (!base || base.endsWith('.sql')) continue
    if (!out.includes(base)) out.push(base)
  }
  return out
}
