import type { TokenProvider } from './api.ts';
import {
  array,
  choice,
  createDailyRequest,
  date,
  input,
  integer,
  invalid,
  nullable,
  pagination,
  record,
  text,
  timestamp,
  uuid,
} from './daily-api.ts';
export type ImportExecution = {
  active_run_id: string | null;
  id: string;
  kind: 'automatic' | 'manual';
  created_at: string;
  since: string | null;
  before: string | null;
  state: string;
  is_current: boolean;
  incremental: boolean;
  discovered_count: number;
  pending_count: number;
  completed_count: number;
  candidate_count: number;
  registered_count: number;
  failed_count: number;
  unknown_count: number;
};
export type ImportExecutionFilter = {
  account_id: string;
  limit: number;
  offset: number;
};
export type ImportExecutionPage = {
  history: ImportExecution[];
  total: number;
  limit: number;
  offset: number;
};
export function importHistoryID(value: unknown): string {
  const id = text(value, 200);
  const match = /^(automation|manual):([0-9a-f-]{36})(?::([1-9][0-9]*))?$/.exec(
    id,
  );
  if (
    !match ||
    !uuid(match[2]) ||
    (match[1] === 'automation'
      ? !match[3] || !Number.isSafeInteger(Number(match[3]))
      : !!match[3])
  )
    return invalid();
  return id;
}
const bool = (v: unknown) => (typeof v === 'boolean' ? v : invalid());
export function parseImportExecution(value: unknown): ImportExecution {
  const v = record(value),
    id = importHistoryID(v.id),
    kind = choice(v.kind, ['automatic', 'manual']),
    since = nullable(v.since, date),
    before = nullable(v.before, date);
  if (
    !id.startsWith((kind === 'automatic' ? 'automation' : 'manual') + ':') ||
    (since === null) !== (before === null) ||
    (since && before && since >= before)
  )
    return invalid();
  return {
    id,
    kind,
    since,
    before,
    created_at: timestamp(v.created_at),
    state: text(v.state, 60),
    is_current: bool(v.is_current),
    incremental: bool(v.incremental),
    discovered_count: integer(v.discovered_count),
    pending_count: integer(v.pending_count),
    completed_count: integer(v.completed_count),
    candidate_count: integer(v.candidate_count),
    registered_count: integer(v.registered_count),
    failed_count: integer(v.failed_count),
    unknown_count: integer(v.unknown_count),
    active_run_id:
      v.active_run_id === undefined ? null : nullable(v.active_run_id, uuid),
  };
}
export function createImportExecutionsAPI(
  base: string,
  fetcher: typeof fetch = fetch,
) {
  const request = createDailyRequest(base, fetcher);
  return {
    async list(
      f: ImportExecutionFilter,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<ImportExecutionPage> {
      const path = input(
        () =>
          `/v1/mail/accounts/${uuid(f.account_id)}/import-history?${new URLSearchParams({ limit: String(integer(f.limit, 1, 100)), offset: String(integer(f.offset)) })}`,
      );
      const v = record(await request(path, token, signal));
      return {
        ...pagination(v),
        history: array(v.history, parseImportExecution),
      };
    },
  };
}
export const importStateNames: Record<string, string> = {
  queued: '開始待ち',
  leased: '開始待ち',
  running: '解析中',
  enabled: '自動取り込み有効',
  completed: '完了',
  stopped: '停止中',
  superseded: '設定変更で終了',
  needs_confirmation: '設定の確認が必要',
  failed: '失敗あり',
  cancelled: '停止済み',
  unknown: '結果確認中',
};
type ProgressCounts = Pick<
  ImportExecution,
  'discovered_count' | 'completed_count' | 'failed_count' | 'unknown_count'
>;
export function importProcessed(row: ProgressCounts) {
  return Math.min(
    row.discovered_count,
    row.completed_count + row.failed_count + row.unknown_count,
  );
}
export function importProgressText(row: ProgressCounts) {
  return `処理済み ${importProcessed(row).toLocaleString('ja-JP')} / ${row.discovered_count.toLocaleString('ja-JP')} 通`;
}
export function importPeriod(
  row: Pick<ImportExecution, 'since' | 'before' | 'incremental'>,
) {
  if (!row.since || !row.before) return '期間指定なし';
  const end = new Date(`${row.before}T00:00:00Z`);
  end.setUTCDate(end.getUTCDate() - 1);
  return `${row.since} 〜 ${end.toISOString().slice(0, 10)}${row.incremental ? '・以降の新着' : ''}`;
}
