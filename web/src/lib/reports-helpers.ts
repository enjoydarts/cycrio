import type { Report, ReportContract } from './reports-api.ts';
const scale = 10n ** 12n;
function units(value: string): bigint {
  const negative = value.startsWith('-'),
    [whole, fraction = ''] = (negative ? value.slice(1) : value).split('.');
  return (
    BigInt(negative ? -1 : 1) *
    (BigInt(whole) * scale + BigInt(fraction.padEnd(12, '0')))
  );
}
function amount(value: bigint): string {
  const negative = value < 0n,
    absolute = negative ? -value : value,
    fraction = String(absolute % scale)
      .padStart(12, '0')
      .replace(/0+$/, '');
  return `${negative ? '-' : ''}${absolute / scale}${fraction ? `.${fraction}` : ''}`;
}
export const addAmounts = (values: string[]): string =>
  amount(values.reduce((total, value) => total + units(value), 0n));
export const subtractAmounts = (a: string, b: string): string =>
  amount(units(a) - units(b));
export function displayAmount(
  value: string | null,
  currency: string | null,
): string {
  if (value === null) return '金額不明';
  const [whole, fraction] = value.split('.');
  return `${whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',')}${fraction ? `.${fraction}` : ''}${currency ? ` ${currency}` : '（通貨不明）'}`;
}
// Round only reference figures for display; all calculations keep twelve decimals.
export function displayEstimate(
  value: string | null,
  currency: string | null,
): string {
  if (value === null) return displayAmount(value, currency);
  const exact = units(value),
    absolute = exact < 0n ? -exact : exact,
    cent = scale / 100n;
  const rounded =
    ((absolute + cent / 2n) / cent) * cent * (exact < 0n ? -1n : 1n);
  return `${rounded === exact ? '' : '≈ '}${displayAmount(amount(rounded), currency)}`;
}
export function upcomingWithin<T extends { date: string }>(
  rows: T[],
  asOf: string,
  days: number,
): T[] {
  const end = new Date(`${asOf}T00:00:00Z`);
  end.setUTCDate(end.getUTCDate() + days);
  const limit = end.toISOString().slice(0, 10);
  return rows.filter((x) => x.date >= asOf && x.date < limit);
}
type SavingContract = {
  id: string;
  currency: string | null;
  monthly_amount: string | null;
  annual_amount: string | null;
};
export function savings(contracts: SavingContract[], selected: string[]) {
  const groups = new Map<
    string,
    { currency: string; monthly_amount: string; annual_amount: string }
  >();
  for (const row of contracts) {
    if (
      !selected.includes(row.id) ||
      !row.currency ||
      row.monthly_amount === null ||
      row.annual_amount === null
    )
      continue;
    const previous = groups.get(row.currency);
    groups.set(row.currency, {
      currency: row.currency,
      monthly_amount: addAmounts([
        previous?.monthly_amount ?? '0',
        row.monthly_amount,
      ]),
      annual_amount: addAmounts([
        previous?.annual_amount ?? '0',
        row.annual_amount,
      ]),
    });
  }
  return [...groups.values()].sort((a, b) =>
    a.currency.localeCompare(b.currency),
  );
}
export function csvCell(value: unknown): string {
  const s = value === null || value === undefined ? '' : String(value);
  return `"${(/^[\s]*[=+\-@\t\r\n]/.test(s) ? `'${s}` : s).replace(/"/g, '""')}"`;
}
export function reportsCSV(report: Report): string {
  const rows: unknown[][] = [
    [
      '種別',
      '対象月',
      'サービス',
      'プラン',
      '通貨',
      '支払額',
      '返金額',
      '差引額',
      '請求額（支払と別）',
      '支払件数',
      '返金件数',
      '請求件数',
    ],
  ];
  for (const bucket of report.history)
    for (const x of bucket.currencies)
      rows.push([
        '月別実績',
        bucket.month,
        '',
        '',
        x.currency,
        x.payment_amount,
        x.refund_amount,
        x.net_amount,
        x.invoice_amount,
        x.payment_count,
        x.refund_count,
        x.invoice_count,
      ]);
  for (const x of report.monthly_services)
    rows.push([
      'サービス別実績',
      report.month,
      x.service,
      x.plan,
      x.currency,
      x.payment_amount,
      x.refund_amount,
      x.net_amount,
      x.invoice_amount,
      x.payment_count,
      x.refund_count,
      x.invoice_count,
    ]);
  return (
    '\uFEFF' +
    rows.map((row) => row.map(csvCell).join(',')).join('\r\n') +
    '\r\n'
  );
}
export function chartPercent(value: string | null, maximum: string): number {
  if (value === null) return 0;
  const v = units(value),
    max = units(maximum);
  return max > 0n ? Number(((v < 0n ? -v : v) * 10000n) / max) / 100 : 0;
}
export function maxAmount(values: (string | null)[]): string {
  return amount(
    values.reduce<bigint>((max, v) => {
      const n = v === null ? 0n : units(v);
      const abs = n < 0n ? -n : n;
      return abs > max ? abs : max;
    }, 0n),
  );
}
export function serviceBreakdown(contracts: ReportContract[]) {
  const groups = new Map<
    string,
    {
      service: string;
      currency: string | null;
      monthly_amount: string | null;
      annual_amount: string | null;
      count: number;
      unpriced: number;
      unknown: number;
    }
  >();
  for (const row of contracts) {
    if (row.status === 'ended' || row.contract_kind === 'fixed_term') continue;
    const key = JSON.stringify([row.service, row.currency]),
      previous = groups.get(key);
    groups.set(key, {
      service: row.service,
      currency: row.currency,
      monthly_amount:
        row.monthly_amount === null
          ? (previous?.monthly_amount ?? null)
          : addAmounts([previous?.monthly_amount ?? '0', row.monthly_amount]),
      annual_amount:
        row.annual_amount === null
          ? (previous?.annual_amount ?? null)
          : addAmounts([previous?.annual_amount ?? '0', row.annual_amount]),
      count: (previous?.count ?? 0) + 1,
      unpriced:
        (previous?.unpriced ?? 0) + (row.monthly_amount === null ? 1 : 0),
      unknown: (previous?.unknown ?? 0) + (row.status === 'unknown' ? 1 : 0),
    });
  }
  return [...groups.values()].sort((a, b) => {
    const byCurrency = (a.currency ?? '').localeCompare(b.currency ?? '');
    if (byCurrency) return byCurrency;
    if (a.monthly_amount === null && b.monthly_amount !== null) return 1;
    if (b.monthly_amount === null && a.monthly_amount !== null) return -1;
    const aValue = units(a.monthly_amount ?? '0'),
      bValue = units(b.monthly_amount ?? '0');
    return aValue > bValue
      ? -1
      : aValue < bValue
        ? 1
        : a.service.localeCompare(b.service, 'ja');
  });
}
