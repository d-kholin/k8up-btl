import type { K8sObject } from '../api'

export type LiveJobKind = 'backup' | 'restore' | 'check' | 'prune'

export type LiveJob = {
  kind: LiveJobKind
  namespace: string
  name: string
  createdAt?: string
  finished: boolean
  failed: boolean
  message?: string
}

type Condition = { type?: string; status?: string; reason?: string; message?: string }

// Mirrors the backend history recorder's reading of K8up CR status.conditions
// so the live-jobs strip agrees with the recorded outcome.
export function liveJobFromCR(kind: LiveJobKind, j: K8sObject): LiveJob {
  const status = (j.status || {}) as { finished?: boolean; conditions?: Condition[] }
  let finished = status.finished === true
  let failed = false
  let message: string | undefined
  for (const c of status.conditions || []) {
    if (c.type === 'Failed' && c.status === 'True') {
      finished = true
      failed = true
      if (c.message) message = c.message
    } else if (c.type === 'Completed' && c.status === 'True') {
      finished = true
      if (c.reason === 'Failed' || c.reason === 'Error') failed = true
      if (!message && c.message) message = c.message
    } else if (
      c.type === 'Progressing' &&
      c.status === 'False' &&
      c.reason === 'Finished' &&
      (c.message || '').toLowerCase().includes('fail')
    ) {
      finished = true
      failed = true
      if (!message && c.message) message = c.message
    }
  }
  return {
    kind,
    namespace: j.namespace || '',
    name: j.name || '',
    createdAt: j.creationTimestamp,
    finished,
    failed,
    message,
  }
}

export function flattenLiveJobs(jobs: Record<string, K8sObject[] | { error: string }>): LiveJob[] {
  const kinds: Array<[string, LiveJobKind]> = [
    ['backups', 'backup'],
    ['restores', 'restore'],
    ['checks', 'check'],
    ['prunes', 'prune'],
  ]
  const out: LiveJob[] = []
  for (const [key, kind] of kinds) {
    const val = jobs[key]
    if (Array.isArray(val)) for (const j of val) out.push(liveJobFromCR(kind, j))
  }
  return out
}
