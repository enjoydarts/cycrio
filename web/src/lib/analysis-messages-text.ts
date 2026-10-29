import type { AnalysisMessage, StageState } from './analysis-messages-api.ts';
export const stageNames: Record<StageState, string> = {
  not_started: '未開始',
  waiting: '待機中',
  running: '処理中',
  completed: '完了',
  skipped: '対象外',
  needs_review: '要確認',
  failed: '失敗',
  unknown: '結果不明',
  cancelled: '取消済み',
};
export const filterNames = {
  all: 'すべて',
  primary_waiting: '一次判定待ち',
  primary_running: '一次判定中',
  secondary_waiting: '二次抽出待ち',
  secondary_running: '二次抽出中',
  completed: '処理完了',
  attention: '要対応',
  not_related: 'サブスク対象外',
};
export function messageName(m: AnalysisMessage) {
  if (m.service) return m.service;
  if (m.primary === 'not_started' && m.error_code) {
    if (m.error_code === 'input_too_large')
      return '処理できるサイズを超えたメール';
    if (
      [
        'source_missing',
        'authentication_failed',
        'tls_failed',
        'connection_failed',
        'credentials_unavailable',
        'timeout',
      ].includes(m.error_code)
    )
      return 'メールを取得できませんでした';
    if (m.error_code === 'unsupported_content')
      return '処理できない形式のメール';
    return '処理の開始を確認してください';
  }
  if (m.secondary === 'skipped') return 'サブスク対象外のメール';
  if (m.primary === 'completed')
    return m.secondary === 'waiting'
      ? '二次抽出待ち'
      : m.secondary === 'running'
        ? '二次抽出中'
        : '一次判定済みのメール';
  return m.primary === 'running'
    ? '一次判定中のメール'
    : m.primary === 'failed'
      ? '一次判定に失敗したメール'
      : m.primary === 'unknown'
        ? '判定結果が不明のメール'
        : '判定待ちのメール';
}
