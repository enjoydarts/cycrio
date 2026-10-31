import type { ProcessingEvent, ProcessingRun } from './mail-processing-api.ts';
export const eventNames: Record<ProcessingEvent['event_type'], string> = {
  invoice: '請求',
  payment: '支払い',
  renewal: '更新',
  refund: '返金',
  change: '変更',
  cancellation: '解約',
  trial: 'お試し',
  payment_failed: '支払い失敗',
  payment_reminder: '支払い督促',
  suspension: '利用停止',
};
export const intervalNames = {
  day: '日',
  week: '週間',
  month: 'か月',
  year: '年',
};
export const cycle = (
  unit: keyof typeof intervalNames | null,
  count: number | null,
) =>
  unit !== null && count !== null
    ? `${count}${intervalNames[unit]}ごと`
    : unit !== null
      ? `回数不明・${intervalNames[unit]}単位`
      : count !== null
        ? `単位不明・回数${count}`
        : '周期不明';
export const money = (amount: string | null, currency: string | null) =>
  amount === null
    ? currency
      ? `金額不明 ${currency}`
      : '金額不明'
    : `${amount} ${currency ?? '通貨不明'}`;
export const runNames: Record<ProcessingRun['state'], string> = {
  queued: '解析待ち',
  leased: '解析の準備中',
  running: '解析中',
  completed: '解析完了',
  failed: '実行失敗',
  cancelled: '停止済み',
};
