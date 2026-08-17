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
  const paths = snapSpec(s).paths || []
  return (
    paths
      .map((p) => p.replace(/\/+$/, '').split('/').filter(Boolean).pop() || '')
      .find((b) => b && !b.endsWith('.sql')) || ''
  )
}
