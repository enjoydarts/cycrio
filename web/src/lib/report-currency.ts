import type {
  Report,
  ActualCurrency,
  ReportCurrency,
  MonthlyService,
} from './reports-api.ts';
import type { ExchangeRates } from './exchange-rates-api.ts';
import {
  addAmounts,
  subtractAmounts,
  csvCell,
  reportsCSV,
  upcomingWithin,
} from './reports-helpers.ts';
export type CurrencyMode = 'native' | 'jpy';
const scale = 10n ** 12n;
function units(value: string): bigint {
  const negative = value.startsWith('-');
  const [whole, fraction = ''] = (negative ? value.slice(1) : value).split('.');
  return (
    (BigInt(whole) * scale + BigInt(fraction.padEnd(12, '0'))) *
    (negative ? -1n : 1n)
  );
}
export function multiplyAmount(value: string, rate: string): string {
  const product = units(value) * units(rate),
    negative = product < 0n;
  const rounded = ((negative ? -product : product) + scale / 2n) / scale;
  const fraction = String(rounded % scale)
    .padStart(12, '0')
    .replace(/0+$/, '');
  return `${negative && rounded !== 0n ? '-' : ''}${rounded / scale}${fraction ? `.${fraction}` : ''}`;
}
const sum = (values: (string | null)[]) => {
  const known = values.filter((x): x is string => x !== null);
  return known.length ? addAmounts(known) : null;
};
const actualKeys = [
  'payment_amount',
  'refund_amount',
  'net_amount',
  'invoice_amount',
] as const;
const countKeys = ['payment_count', 'refund_count', 'invoice_count'] as const;
function combineActual<T extends ActualCurrency>(rows: T[]): T[] {
  if (!rows.length) return [];
  const first = { ...rows[0] };
  for (const key of actualKeys) first[key] = sum(rows.map((x) => x[key]));
  for (const key of countKeys)
    first[key] = rows.reduce((n, x) => n + x[key], 0);
  if (first.net_amount !== null)
    first.net_amount = subtractAmounts(
      first.payment_amount ?? '0',
      first.refund_amount ?? '0',
    );
  return [first];
}
export function projectReport(native: Report, rates: ExchangeRates) {
  const lookup = new Map(rates.rates.map((x) => [x.currency, x.rate]));
  const supported = (currency: string | null) =>
    currency !== null && lookup.has(currency);
  const convert = (value: string | null, currency: string | null) =>
    value !== null && currency !== null && lookup.has(currency)
      ? multiplyAmount(value, lookup.get(currency)!)
      : null;
  const actual = <T extends ActualCurrency>(row: T): T => {
    const converted = {
      ...row,
      currency: 'JPY',
      ...Object.fromEntries(
        actualKeys.map((key) => [key, convert(row[key], row.currency)]),
      ),
    };
    if (converted.net_amount !== null)
      converted.net_amount = subtractAmounts(
        converted.payment_amount ?? '0',
        converted.refund_amount ?? '0',
      );
    return converted;
  };
  const contracts: Report['contracts'] = native.contracts.map((x) => ({
    ...x,
    currency: supported(x.currency) ? 'JPY' : x.currency,
    amount: convert(x.amount, x.currency),
    monthly_amount: convert(x.monthly_amount, x.currency),
    annual_amount: convert(x.annual_amount, x.currency),
    exclusion_reason:
      !supported(x.currency) && x.exclusion_reason === ''
        ? 'missing_currency'
        : x.exclusion_reason,
  }));
  const upcoming: Report['upcoming'] = native.upcoming
    .filter((x) => supported(x.currency))
    .map((x) => ({
      ...x,
      currency: 'JPY',
      amount: convert(x.amount, x.currency),
    }));
  const normalized = contracts.filter((x) => x.exclusion_reason === '');
  const next30 = upcomingWithin(upcoming, native.as_of, 30);
  const currencyRows = native.currencies.filter((x) => supported(x.currency));
  const currencies: ReportCurrency[] = currencyRows.length
    ? [
        {
          currency: 'JPY',
          monthly_amount: sum(normalized.map((x) => x.monthly_amount)),
          annual_amount: sum(normalized.map((x) => x.annual_amount)),
          contract_count: normalized.length,
          next_30_days_amount: sum(next30.map((x) => x.amount)),
          next_30_days_count: next30.length,
        },
      ]
    : [];
  const groups = new Map<string, MonthlyService[]>();
  for (const row of native.monthly_services.filter((x) =>
    supported(x.currency),
  ))
    groups.set(row.subscription_id, [
      ...(groups.get(row.subscription_id) ?? []),
      actual(row),
    ]);
  const report: Report = {
    ...native,
    coverage: {
      ...native.coverage,
      normalized_contracts: native.contracts.filter(
        (x) => x.exclusion_reason === '' && supported(x.currency),
      ).length,
    },
    contracts,
    currencies,
    upcoming,
    history: native.history.map((x) => ({
      ...x,
      currencies: combineActual(
        x.currencies.filter((x) => supported(x.currency)).map(actual),
      ),
    })),
    monthly_services: [...groups.values()].flatMap(combineActual),
  };
  return {
    report,
    excluded_contracts: native.contracts.filter(
      (x) =>
        x.status !== 'ended' &&
        x.contract_kind !== 'fixed_term' &&
        (!supported(x.currency) || x.amount === null),
    ).length,
    excluded_currency_rows: native.currencies.filter(
      (x) => !supported(x.currency),
    ).length,
    excluded_actual_rows: native.history.reduce(
      (n, x) =>
        n +
        x.currencies.filter(
          (x) =>
            !supported(x.currency) &&
            (actualKeys.some((key) => x[key] !== null) ||
              countKeys.some((key) => x[key] > 0)),
        ).length,
      0,
    ),
    excluded_upcoming: native.upcoming.filter(
      (x) => !supported(x.currency) || x.amount === null,
    ).length,
  };
}
export function currencyCSV(
  native: Report,
  mode: CurrencyMode,
  rates: ExchangeRates | null,
): string {
  if (mode === 'jpy' && !rates) throw new Error('最新為替レートが必要です');
  const metadata: unknown[][] = [
    [
      '表示通貨',
      mode === 'jpy'
        ? '円換算（最新レートによる参考換算・過去支払時のレートではありません）'
        : '元通貨',
    ],
  ];
  if (rates) {
    metadata.push(
      ['為替取得元', rates.source, rates.source_url],
      ['為替基準日', rates.as_of],
      ['為替取得日時', rates.fetched_at],
    );
    for (const x of rates.rates)
      metadata.push(['為替レート（1元通貨あたりJPY）', x.currency, x.rate]);
  }
  const report = mode === 'jpy' ? projectReport(native, rates!).report : native;
  if (mode === 'jpy') {
    const coverage = projectReport(native, rates!);
    metadata.push(
      ['換算対象外契約', coverage.excluded_contracts],
      ['換算対象外実績通貨行', coverage.excluded_actual_rows],
      ['換算対象外請求予定', coverage.excluded_upcoming],
    );
  }
  const prefix =
    metadata.map((row) => row.map(csvCell).join(',')).join('\r\n') + '\r\n';
  let csv = '\uFEFF' + prefix + reportsCSV(report).slice(1);
  if (mode === 'jpy')
    csv +=
      '\r\n' +
      [['元通貨・元金額（保存された実績）'].map(csvCell).join(',')].join('') +
      '\r\n' +
      reportsCSV(native).slice(1);
  return csv;
}
