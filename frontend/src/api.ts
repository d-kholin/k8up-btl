export type User = { username: string; email?: string }

export type K8sObject = {
  apiVersion?: string
  kind?: string
  namespace?: string
  name?: string
  labels?: Record<string, string>
  creationTimestamp?: string
  spec?: Record<string, unknown>
  status?: Record<string, unknown>
}

export type RestoreState = {
  restoreId: string
  step: string
  snapshotId?: string
  snapshotNamespace?: string
  pvcNamespace?: string
  pvcName?: string
  lastError?: string
  argoSyncResumed?: boolean
  argoPausedGlobally?: boolean
  argoControllerReplicas?: number
  application?: { namespace: string; name: string }
  startedAt?: string
  finishedAt?: string
  originalReplicas?: number
  restoreCRName?: string
  cancelRequested?: boolean
  cancelled?: boolean
  progressPercent?: number
  bytesRecovered?: number
  // SQL dump recovery fields (kind === 'recovery')
  kind?: string
  dbPod?: string
  dbContainer?: string
  dbWorkload?: WorkloadRef
  restoreCommand?: string
  dumpPath?: string
  stoppedWorkloads?: ScalableWorkload[]
  pvcParts?: PVCRestorePart[]
  safetyBackupCR?: string
}

export type WorkloadRef = { kind: string; namespace: string; name: string }
export type ScalableWorkload = WorkloadRef & { replicas: number }

export type PVCRestorePart = {
  snapshotName: string
  snapshotId: string
  pvcName: string
  restoreCRName?: string
  status?: 'pending' | 'running' | 'done' | 'failed'
  workload?: WorkloadRef
}

export type RecoveryDBCandidate = {
  podName: string
  container: string
  workload?: WorkloadRef
  instance?: string
  backupCommand: string
  restoreCommand?: string
  commandSource?: string
  commandError?: string
  workloadsToStop: ScalableWorkload[]
  quiesceWarning?: string
  hasRestoreCommand: boolean
}

export type RecoveryPVCOption = {
  pvcName: string
  snapshotName: string
  snapshotId: string
  date: string
  deltaSeconds: number
}

export type RecoveryPlan = {
  snapshot: { namespace: string; name: string; dumpPath: string; date: string }
  dbCandidates: RecoveryDBCandidate[]
  bestGuess: string
  pvcOptions: RecoveryPVCOption[]
}

export type ClusterStatus = {
  connected: boolean
  error?: string
  checkedAt?: string
}

export type Meta = {
  grafanaDashboardUrl?: string
  prometheusConfigured: boolean
  argocdNamespace: string
  notifyChannels?: string[]
  cluster?: ClusterStatus
}

export type AuditPage = {
  entries: AuditEntry[]
  total: number
  limit: number
  offset: number
}

export type AuditSummary = {
  restoreOk: number
  restoreFail: number
  downloadBytes: number
  latestRestore?: AuditEntry
}

export type NotifyFieldView = {
  ntfyServer: string
  ntfyTopic: string
  ntfyTokenSet: boolean
  smtpHost: string
  smtpPort: number
  smtpTls: string
  smtpUser: string
  smtpPassSet: boolean
  smtpFrom: string
  smtpTo: string
}

export type NotifySettings = {
  env: NotifyFieldView
  overrides: NotifyFieldView & { ntfyDisabled: boolean; emailDisabled: boolean }
  channels: string[]
}

// Merge-style update: absent field = unchanged, '' / 0 / false = clear override.
export type NotifySettingsUpdate = Partial<{
  ntfyServer: string
  ntfyTopic: string
  ntfyToken: string
  ntfyDisabled: boolean
  smtpHost: string
  smtpPort: number
  smtpTls: string
  smtpUser: string
  smtpPass: string
  smtpFrom: string
  smtpTo: string
  emailDisabled: boolean
}>

export type AuditEntry = {
  id: number
  kind: string
  actor: string
  at: string
  namespace?: string
  snapshot?: string
  pvc?: string
  path?: string
  status?: string
  detail?: string
  bytes?: number
  restoreId?: string
  argoApp?: string
  argoPaused?: boolean
}

export type BackupEvent = {
  uid: string
  kind: string
  namespace: string
  name: string
  schedule?: string
  status: 'running' | 'succeeded' | 'failed' | 'unknown'
  message?: string
  startedAt: string
  finishedAt?: string
}

export type PVCRef = { namespace: string; name: string }

export type FileNode = {
  name: string
  type: string
  path: string
  size?: number
  mtime?: string
}

export type DiffChangeKind = 'added' | 'removed' | 'modified' | 'metadata'

export type DiffChange = {
  path: string
  modifier: string
  kind: DiffChangeKind
}

export type SnapshotDiff = {
  base: { name: string; id: string }
  target: { name: string; id: string }
  changedFiles: number
  added: { files: number; dirs: number; bytes: number }
  removed: { files: number; dirs: number; bytes: number }
  changes: DiffChange[]
  totalChanges: number
  truncated: boolean
}

export type StorageStats = {
  collectedAt: string
  logicalBytes: number
  storedBytes: number
  savedBytes: number
  dedupRatio: number
  snapshotCount: number
  partial?: boolean
  stale?: boolean
  computing?: boolean
  cacheAgeSec?: number
  error?: string
  repos: Array<{
    namespace: string
    repository: string
    logicalBytes: number
    storedBytes: number
    snapshotCount: number
    dedupRatio: number
    error?: string
  }>
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response
  try {
    res = await fetch(path, {
      ...init,
      headers: {
        'Content-Type': 'application/json',
        ...(init?.headers || {}),
      },
    })
  } catch (e) {
    // Normalize aborts so callers can ignore superseded navigations.
    if (init?.signal?.aborted) {
      const err = new Error('Aborted')
      err.name = 'AbortError'
      throw err
    }
    throw e
  }
  if (!res.ok) {
    const text = await res.text()
    throw new Error(text || res.statusText)
  }
  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

export const api = {
  me: () => req<User>('/api/v1/me'),
  meta: () => req<Meta>('/api/v1/meta'),
  schedules: () => req<K8sObject[]>('/api/v1/schedules'),
  snapshots: (namespace?: string) =>
    req<K8sObject[]>(`/api/v1/snapshots${namespace ? `?namespace=${encodeURIComponent(namespace)}` : ''}`),
  jobs: () => req<Record<string, K8sObject[] | { error: string }>>('/api/v1/jobs'),
  restores: () => req<RestoreState[]>('/api/v1/restores'),
  restore: (id: string) => req<RestoreState>(`/api/v1/restores/${id}`),
  restoreLogs: (id: string) =>
    req<{ restoreId: string; lines: string[] }>(`/api/v1/restores/${id}/logs`),
  storageStats: (refresh = false) =>
    req<StorageStats>(`/api/v1/stats/storage${refresh ? '?refresh=1' : ''}`),
  startRestore: (body: {
    snapshotNamespace: string
    snapshotName: string
    snapshotId?: string
    pvcNamespace: string
    pvcName: string
  }) =>
    req<RestoreState>('/api/v1/restores', { method: 'POST', body: JSON.stringify(body) }),
  cancelRestore: (id: string) =>
    req<{ status: string }>(`/api/v1/restores/${id}/cancel`, { method: 'POST' }),
  recoveryPlan: (ns: string, name: string) =>
    req<RecoveryPlan>(`/api/v1/snapshots/${ns}/${name}/recovery-plan`),
  startRecovery: (body: {
    namespace: string
    dumpSnapshotName: string
    dbPodName: string
    skipSafetyBackup: boolean
    pvcRestores: Array<{ snapshotName: string; pvcName: string }>
  }) =>
    req<RestoreState>('/api/v1/recoveries', { method: 'POST', body: JSON.stringify(body) }),
  interrupted: () => req<RestoreState[]>('/api/v1/interrupted'),
  resumeArgo: (ns: string, name: string) =>
    req<{ status: string }>(`/api/v1/argo/${ns}/${name}/resume`, { method: 'POST' }),
  files: (ns: string, name: string, path = '/', signal?: AbortSignal) =>
    req<FileNode[]>(`/api/v1/snapshots/${ns}/${name}/files?path=${encodeURIComponent(path)}`, {
      signal,
    }),
  downloadUrl: (ns: string, name: string, path: string, opts?: { archive?: 'zip' | 'tar'; folder?: boolean }) => {
    const q = new URLSearchParams()
    q.set('path', path || '/')
    if (opts?.archive) q.set('archive', opts.archive)
    if (opts?.folder) q.set('folder', '1')
    return `/api/v1/snapshots/${ns}/${name}/download?${q.toString()}`
  },
  diff: (ns: string, name: string, base: string, signal?: AbortSignal) =>
    req<SnapshotDiff>(
      `/api/v1/snapshots/${ns}/${name}/diff?base=${encodeURIComponent(base)}`,
      { signal },
    ),
  pvcs: () => req<PVCRef[]>('/api/v1/pvcs'),
  backupHistory: (days = 366, kind?: string) =>
    req<{ since: string; events: BackupEvent[] }>(
      `/api/v1/history/backups?days=${days}${kind ? `&kind=${encodeURIComponent(kind)}` : ''}`,
    ),
  audit: (opts?: {
    kind?: string
    actor?: string
    status?: string
    since?: string
    until?: string
    limit?: number
    offset?: number
  }) => {
    const q = new URLSearchParams()
    if (opts?.kind) q.set('kind', opts.kind)
    if (opts?.actor) q.set('actor', opts.actor)
    if (opts?.status) q.set('status', opts.status)
    if (opts?.since) q.set('since', opts.since)
    if (opts?.until) q.set('until', opts.until)
    if (opts?.limit) q.set('limit', String(opts.limit))
    if (opts?.offset) q.set('offset', String(opts.offset))
    const qs = q.toString()
    return req<AuditPage>(`/api/v1/audit${qs ? `?${qs}` : ''}`)
  },
  auditSummary: () => req<AuditSummary>('/api/v1/audit/summary'),
  auditExportUrl: (opts?: { kind?: string; actor?: string; since?: string; until?: string }) => {
    const q = new URLSearchParams()
    if (opts?.kind) q.set('kind', opts.kind)
    if (opts?.actor) q.set('actor', opts.actor)
    if (opts?.since) q.set('since', opts.since)
    if (opts?.until) q.set('until', opts.until)
    const qs = q.toString()
    return `/api/v1/audit/export.csv${qs ? `?${qs}` : ''}`
  },
  notifySettings: () => req<NotifySettings>('/api/v1/settings/notifications'),
  updateNotifySettings: (body: NotifySettingsUpdate) =>
    req<NotifySettings>('/api/v1/settings/notifications', {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
  notifyTest: () =>
    req<{ ok: boolean; channels: Record<string, string> }>('/api/v1/notify/test', {
      method: 'POST',
    }),
  createBackup: (namespace: string, spec: Record<string, unknown> = {}) =>
    req<K8sObject>('/api/v1/backups', { method: 'POST', body: JSON.stringify({ namespace, spec }) }),
  createCheck: (namespace: string, spec: Record<string, unknown> = {}) =>
    req<K8sObject>('/api/v1/checks', { method: 'POST', body: JSON.stringify({ namespace, spec }) }),
}
