import assert from 'node:assert/strict';
import test from 'node:test';
import { parseReport } from './reports-api.ts';
import { reportFixture, reportContractFixture } from './reports-fixtures.ts';
import {
  multiplyAmount,
  projectReport,
  currencyCSV,
} from './report-currency.ts';
import {
  parseExchangeRates,
  createExchangeRatesAPI,
  createExchangeRatesController,
} from './exchange-rates-api.ts';
import { ApiError } from './api.ts';
import { ratesFixture } from './exchange-rates-fixtures.ts';
import { savings, subtractAmounts, addAmounts } from './reports-helpers.ts';

test('rounded summaries equal visible contract and due sums, and selecting all leaves exactly zero', () => {
  const native = parseReport(reportFixture());
  native.contracts = Array.from({ length: 2 }, (_, i) => ({
    id: `contract-${i}`,
    service: 'Tiny',
    plan: null,
    status: 'active',
    contract_kind: 'recurring',
    amount: '0.00000001',
    currency: 'USD',
    interval_unit: 'month',
    interval_count: 1,
    next_billing_date: '2026-11-03',
    monthly_amount: '0.00000001',
    annual_amount: '0.00000012',
    exclusion_reason: '',
  }));
  native.currencies = [
    {
      currency: 'USD',
      monthly_amount: '0.00000002',
      annual_amount: '0.00000024',
      contract_count: 2,
      next_30_days_amount: '0.00000002',
      next_30_days_count: 2,
    },
  ];
  native.upcoming = native.contracts.map((x) => ({
    subscription_id: x.id,
    service: x.service,
    plan: null,
    date: '2026-11-03',
    currency: 'USD',
    amount: x.amount,
    annual_renewal: false,
    status: 'active',
  }));
  const rates = ratesFixture();
  rates.rates.find((x) => x.currency === 'USD')!.rate = '1.00005';
  const { report } = projectReport(native, rates);
  const summary = report.currencies[0],
    all = savings(
      report.contracts,
      report.contracts.map((x) => x.id),
    )[0];
  assert.equal(summary.monthly_amount, '0.000000020002');
  assert.equal(summary.monthly_amount, all.monthly_amount);
  assert.equal(summary.annual_amount, all.annual_amount);
  assert.equal(
    subtractAmounts(summary.monthly_amount!, all.monthly_amount),
    '0',
  );
  assert.equal(summary.contract_count, 2);
  assert.equal(
    summary.next_30_days_amount,
    addAmounts(report.upcoming.map((x) => x.amount!)),
  );
});
test('converted actual net always matches converted payment minus refund at rounding boundaries', () => {
  const native = parseReport(reportFixture());
  const actual = {
    currency: 'USD',
    payment_amount: '0.00000002',
    refund_amount: '0.00000001',
    net_amount: '0.00000001',
    invoice_amount: null,
    payment_count: 1,
    refund_count: 1,
    invoice_count: 0,
  };
  native.history[0].currencies = [actual];
  native.monthly_services = [
    { ...actual, subscription_id: 'original-id', service: 'Tiny', plan: null },
  ];
  const rates = ratesFixture();
  rates.rates.find((x) => x.currency === 'USD')!.rate = '1.00005';
  const { report } = projectReport(native, rates);
  for (const row of [
    report.history[0].currencies[0],
    report.monthly_services[0],
  ]) {
    assert.equal(
      row.net_amount,
      subtractAmounts(row.payment_amount!, row.refund_amount!),
    );
    assert.equal(row.net_amount, '0.00000001');
    assert.equal(row.invoice_amount, null);
  }
});

test('decimal multiplication rounds twelve places without Number, including negative actuals and zero', () => {
  assert.equal(
    multiplyAmount('999999999999999999999999', '150.123456789012'),
    '150123456789011999999999849.876543210988',
  );
  assert.equal(multiplyAmount('-0.000000000001', '1.5'), '-0.000000000002');
  assert.equal(multiplyAmount('0', '150'), '0');
});
test('projection combines JPY and supported foreign rows, preserves null history, identity and native data', () => {
  const report = parseReport(reportFixture());
  report.currencies = ['JPY', 'USD', 'GBP'].map((currency) => ({
    currency,
    monthly_amount: '10',
    annual_amount: '120',
    contract_count: 1,
    next_30_days_amount: null,
    next_30_days_count: 0,
  }));
  report.contracts = report.currencies.map((x, i) =>
    reportContractFixture(x.currency, '10', '120', `contract-${i}`),
  );
  report.history[0].currencies = [
    {
      currency: 'USD',
      payment_amount: null,
      refund_amount: '1',
      net_amount: '-1',
      invoice_amount: null,
      payment_count: 0,
      refund_count: 1,
      invoice_count: 0,
    },
  ];
  const snapshot = structuredClone(report);
  const projected = projectReport(report, ratesFixture());
  assert.equal(projected.report.currencies.length, 1);
  assert.equal(
    projected.report.currencies[0].monthly_amount,
    '1511.23456789012',
  );
  assert.equal(projected.report.currencies[0].next_30_days_amount, null);
  assert.equal(projected.report.history[0].currencies[0].payment_amount, null);
  assert.equal(
    projected.report.history[0].currencies[0].net_amount,
    '-150.123456789012',
  );
  assert.deepEqual(report, snapshot);
  assert.equal(projected.excluded_currency_rows, 1);
  assert.deepEqual(
    projectReport({ ...report, currencies: [] }, ratesFixture()).report
      .currencies,
    [],
  );
});
test('unsupported empty history placeholders do not count as excluded actual records, recorded zero does', () => {
  const report = parseReport(reportFixture());
  const empty = {
    currency: 'GBP',
    payment_amount: null,
    refund_amount: null,
    net_amount: null,
    invoice_amount: null,
    payment_count: 0,
    refund_count: 0,
    invoice_count: 0,
  };
  report.history.forEach((x) => x.currencies.push({ ...empty }));
  assert.equal(projectReport(report, ratesFixture()).excluded_actual_rows, 0);
  report.history[0].currencies[0] = {
    ...empty,
    payment_amount: '0',
    net_amount: '0',
    payment_count: 1,
  };
  assert.equal(projectReport(report, ratesFixture()).excluded_actual_rows, 1);
});
test('contracts, upcoming and monthly services convert uniformly while original identities, zero and null survive', () => {
  const report = parseReport(reportFixture());
  const id = '11111111-1111-4111-8111-111111111111';
  report.contracts = [
    {
      id,
      service: 'Service',
      plan: null,
      status: 'active',
      contract_kind: 'recurring',
      amount: '1',
      currency: 'USD',
      interval_unit: 'month',
      interval_count: 1,
      next_billing_date: '2026-11-03',
      monthly_amount: '1',
      annual_amount: '12',
      exclusion_reason: '',
    },
  ];
  report.upcoming = [
    {
      subscription_id: id,
      service: 'Service',
      plan: null,
      date: '2026-11-03',
      amount: '1',
      currency: 'USD',
      annual_renewal: false,
      status: 'active',
    },
  ];
  report.monthly_services = [
    {
      subscription_id: id,
      service: 'Service',
      plan: null,
      currency: 'USD',
      payment_amount: '0',
      refund_amount: '1',
      net_amount: '-1',
      invoice_amount: null,
      payment_count: 1,
      refund_count: 1,
      invoice_count: 0,
    },
  ];
  const projected = projectReport(report, ratesFixture());
  assert.equal(projected.report.contracts[0].id, id);
  assert.equal(projected.report.contracts[0].amount, '150.123456789012');
  assert.equal(projected.report.upcoming[0].amount, '150.123456789012');
  assert.equal(projected.report.monthly_services[0].subscription_id, id);
  assert.equal(projected.report.monthly_services[0].payment_amount, '0');
  assert.equal(
    projected.report.monthly_services[0].refund_amount,
    '150.123456789012',
  );
  assert.equal(projected.report.monthly_services[0].invoice_amount, null);
  assert.equal(
    projected.report.monthly_services[0].net_amount,
    '-150.123456789012',
  );
  assert.equal(report.contracts[0].amount, '1');
  assert.equal(report.contracts[0].currency, 'USD');
  report.contracts[0].currency = 'GBP';
  report.upcoming[0].currency = 'GBP';
  report.coverage.normalized_contracts = 1;
  const excluded = projectReport(report, ratesFixture());
  assert.equal(excluded.report.contracts[0].monthly_amount, null);
  assert.equal(excluded.report.upcoming.length, 0);
  assert.equal(excluded.excluded_contracts, 1);
  assert.equal(excluded.excluded_upcoming, 1);
  assert.equal(excluded.report.coverage.normalized_contracts, 0);
  assert.equal(report.coverage.normalized_contracts, 1);
});
test('JPY only zero remains zero and unsupported-only totals never become false zero', () => {
  const report = parseReport(reportFixture());
  report.contracts = [reportContractFixture('JPY', '0', '0')];
  report.currencies = [
    {
      currency: 'JPY',
      monthly_amount: '0',
      annual_amount: '0',
      contract_count: 1,
      next_30_days_amount: null,
      next_30_days_count: 0,
    },
  ];
  assert.equal(
    projectReport(report, ratesFixture()).report.currencies[0].monthly_amount,
    '0',
  );
  report.currencies[0].currency = 'GBP';
  report.contracts[0].currency = 'GBP';
  assert.deepEqual(projectReport(report, ratesFixture()).report.currencies, []);
});
test('CSV records latest valuation provenance and original units, escaping metadata and service names', () => {
  const report = parseReport(reportFixture());
  report.monthly_services = [
    {
      currency: 'USD',
      subscription_id: 'original-id',
      service: '=BAD',
      plan: null,
      payment_amount: '1',
      refund_amount: null,
      net_amount: '1',
      invoice_amount: null,
      payment_count: 1,
      refund_count: 0,
      invoice_count: 0,
    },
  ];
  const csv = currencyCSV(report, 'jpy', ratesFixture());
  assert.match(csv, /最新レートによる参考換算/);
  assert.match(csv, /2026-11-02/);
  assert.match(csv, /150.123456789012/);
  assert.match(csv, /元通貨/);
  assert.match(csv, /USD/);
  assert.match(csv, /"'=BAD"/);
});
test('rate response validation requires exact supported currency set, positive decimal rates and JPY base', () => {
  assert.deepEqual(parseExchangeRates(ratesFixture()), ratesFixture());
  for (const patch of [
    { base_currency: 'EUR' },
    { rates: [{ currency: 'JPY', rate: '1' }] },
    { rates: ratesFixture().rates.map((x) => ({ ...x, rate: '0' })) },
    { source_url: 'https://example.com' },
  ])
    assert.throws(() => parseExchangeRates({ ...ratesFixture(), ...patch }));
});
test('FX API requests authenticated fixed endpoint and a fresh load clears failed/stale data', async () => {
  let url = '';
  const api = createExchangeRatesAPI('', async (input, init) => {
    url = String(input);
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer t');
    return new Response(JSON.stringify(ratesFixture()));
  });
  await api.read(async () => 't');
  assert.equal(url, '/v1/exchange-rates');
  let fail = false;
  const c = createExchangeRatesController(
    {
      read: async () => {
        if (fail) throw new Error('offline');
        return ratesFixture();
      },
    },
    async () => 't',
    () => {},
    () => {},
  );
  await c.load();
  assert.equal(c.state().status, 'ready');
  fail = true;
  const pending = c.load();
  assert.equal(c.state().page, null);
  await pending;
  assert.equal(c.state().status, 'error');
  assert.equal(c.state().page, null);
  fail = false;
  await c.load();
  assert.equal(c.state().error, null);
});
test('FX generations, disposal and denial suppress late rates', async () => {
  const gates: ((v: ReturnType<typeof ratesFixture>) => void)[] = [];
  const c = createExchangeRatesController(
    { read: () => new Promise((resolve) => gates.push(resolve)) },
    async () => 't',
    () => {},
    () => {},
  );
  const old = c.load(),
    latest = c.load();
  gates[1](ratesFixture());
  await latest;
  gates[0]({ ...ratesFixture(), as_of: '2026-10-30' });
  await old;
  assert.equal(c.state().page?.as_of, '2026-11-02');
  const pending = c.load();
  c.dispose();
  gates[2](ratesFixture());
  await pending;
  assert.equal(c.state().page, null);
  let denied = 0;
  const blocked = createExchangeRatesController(
    {
      read: async () => {
        throw new ApiError('access_denied');
      },
    },
    async () => 't',
    () => {},
    () => denied++,
  );
  await blocked.load();
  await blocked.load();
  assert.equal(denied, 1);
  assert.equal(blocked.state().page, null);
});
