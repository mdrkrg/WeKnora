export interface DataSourceSyncStartedEvent {
  kbId: string
  dataSourceId: string
  syncLogId: string
}

interface SyncLogSnapshot {
  id?: string
  status?: string
}

interface DataSourceSnapshot {
  latest_sync_log?: SyncLogSnapshot | null
}

export type DataSourceSyncMonitorResult = 'success' | 'partial' | 'canceled' | 'timeout' | 'aborted'

export function isTargetSyncSettled(
  log: SyncLogSnapshot | null | undefined,
  targetSyncLogId: string,
): log is SyncLogSnapshot & { status: 'success' | 'partial' | 'canceled' } {
  if (!log || !targetSyncLogId || log.id !== targetSyncLogId) return false
  return log.status === 'success' || log.status === 'partial' || log.status === 'canceled'
}

function waitForNextPoll(ms: number, signal?: AbortSignal): Promise<void> {
  if (signal?.aborted) return Promise.resolve()
  return new Promise(resolve => {
    const timer = setTimeout(resolve, ms)
    signal?.addEventListener('abort', () => {
      clearTimeout(timer)
      resolve()
    }, { once: true })
  })
}

export async function monitorDataSourceSync(options: {
  targetSyncLogId: string
  fetchDataSource: () => Promise<DataSourceSnapshot>
  refreshKnowledge: () => Promise<unknown>
  signal?: AbortSignal
  maxPolls?: number
  intervalMs?: number
  wait?: (ms: number, signal?: AbortSignal) => Promise<void>
}): Promise<DataSourceSyncMonitorResult> {
  const maxPolls = options.maxPolls ?? 100
  const intervalMs = options.intervalMs ?? 3000
  const wait = options.wait ?? waitForNextPoll

  for (let poll = 0; poll < maxPolls; poll++) {
    if (options.signal?.aborted) return 'aborted'

    let snapshot: DataSourceSnapshot | null = null
    try {
      snapshot = await options.fetchDataSource()
    } catch {
      // A transient API failure should not stop monitoring the background sync.
    }

    if (options.signal?.aborted) return 'aborted'
    await options.refreshKnowledge()

    const log = snapshot?.latest_sync_log
    if (isTargetSyncSettled(log, options.targetSyncLogId)) {
      return log.status
    }

    if (poll + 1 < maxPolls) {
      await wait(intervalMs, options.signal)
    }
  }

  return options.signal?.aborted ? 'aborted' : 'timeout'
}
