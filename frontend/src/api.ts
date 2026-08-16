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
}

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

export type FileNode = {
  name: string
  type: string
  path: string
  size?: number
  mtime?: string
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
  meta: () => req<{ grafanaDashboardUrl?: string; prometheusConfigured: boolean; argocdNamespace: string }>('/api/v1/meta'),
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
  interrupted: () => req<RestoreState[]>('/api/v1/interrupted'),
  resumeArgo: (ns: string, name: string) =>
    req<{ status: string }>(`/api/v1/argo/${ns}/${name}/resume`, { method: 'POST' }),
  files: (ns: string, name: string, path = '/', signal?: AbortSignal) =>
    req<FileNode[]>(`/api/v1/snapshots/${ns}/${name}/files?path=${encodeURIComponent(path)}`, {
      signal,
    }),
  downloadUrl: (ns: string, name: string, path: string) =>
    `/api/v1/snapshots/${ns}/${name}/download?path=${encodeURIComponent(path)}`,
  audit: (kind?: string) =>
    req<AuditEntry[]>(`/api/v1/audit${kind ? `?kind=${encodeURIComponent(kind)}` : ''}`),
  createBackup: (namespace: string, spec: Record<string, unknown> = {}) =>
    req<K8sObject>('/api/v1/backups', { method: 'POST', body: JSON.stringify({ namespace, spec }) }),
  createCheck: (namespace: string, spec: Record<string, unknown> = {}) =>
    req<K8sObject>('/api/v1/checks', { method: 'POST', body: JSON.stringify({ namespace, spec }) }),
}
