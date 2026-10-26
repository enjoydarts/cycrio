import { choice, date, integer, invalid, record } from './daily-api.ts';
export type ScheduleRule = {
  interval_unit: 'day' | 'week' | 'month' | 'year';
  interval_count: number;
  anchor_date: string;
  month_end: boolean;
};
export function parseScheduleRule(value: unknown): ScheduleRule {
  const v = record(value),
    interval_unit = choice(v.interval_unit, [
      'day',
      'week',
      'month',
      'year',
    ] as const),
    anchor_date = date(v.anchor_date),
    month_end = typeof v.month_end === 'boolean' ? v.month_end : invalid();
  if (month_end) {
    if (interval_unit !== 'month' && interval_unit !== 'year') return invalid();
    const [year, month, day] = anchor_date.split('-').map(Number),
      leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
    if (
      day !==
      [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31][month - 1]
    )
      return invalid();
  }
  return {
    interval_unit,
    interval_count: integer(v.interval_count, 1, 10000),
    anchor_date,
    month_end,
  };
}
export const sameScheduleRule = (a: ScheduleRule, b: ScheduleRule) =>
  a.interval_unit === b.interval_unit &&
  a.interval_count === b.interval_count &&
  a.anchor_date === b.anchor_date &&
  a.month_end === b.month_end;
