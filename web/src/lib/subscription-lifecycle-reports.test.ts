import assert from 'node:assert/strict';
import test from 'node:test';
import { parseReport } from './reports-api.ts';
import { serviceBreakdown } from './reports-helpers.ts';
import { reportFixture, reportContractFixture } from './reports-fixtures.ts';
import { parseSchedulePreview } from './schedule-api.ts';
import { subscription } from './subscriptions-fixtures.ts';
import { parseOverview } from './overview-api.ts';
import { overviewFixture } from './overview-fixtures.ts';
test('overview accepts excluded schedules separately from missing dates and defaults older response counts to zero', () => {
  const legacy = overviewFixture();
  delete (legacy.subscriptions as Record<string, unknown>).excluded;
  assert.equal(parseOverview(legacy).subscriptions.excluded, 0);
  const current = {
    ...legacy,
    subscriptions: { ...legacy.subscriptions, overdue: 0, excluded: 1 },
  };
  assert.equal(parseOverview(current).subscriptions.undated, 0);
  assert.equal(parseOverview(current).subscriptions.excluded, 1);
  assert.throws(() =>
    parseOverview({
      ...current,
      subscriptions: { ...current.subscriptions, excluded: 2 },
    }),
  );
});
test('reports exclude access-ended contracts from current cost without changing active status', () => {
  const report = { ...reportFixture(), contracts: [reportContractFixture()] };
  report.contracts[0] = {
    ...reportContractFixture(),
    access_end_date: '2026-10-31',
    trial_end_date: null,
    auto_renew: false,
    monthly_amount: null,
    annual_amount: null,
    exclusion_reason: 'access_ended',
  };
  report.coverage = {
    ...report.coverage,
    total_contracts: 1,
    open_contracts: 1,
    active_contracts: 1,
  };
  const parsed = parseReport(report);
  assert.equal(parsed.contracts[0].status, 'active');
  assert.equal(parsed.contracts[0].auto_renew, false);
  assert.equal(
    serviceBreakdown(parsed.contracts).some(
      (x) => x.service === parsed.contracts[0].service,
    ),
    false,
  );
  assert.throws(() =>
    parseReport({
      ...report,
      contracts: [{ ...report.contracts[0], access_end_date: report.as_of }],
    }),
  );
});
test('schedule preview accepts inclusive access-end exclusions with no generated candidate', () => {
  for (const reason of ['access_ended', 'after_access_end']) {
    const value = parseSchedulePreview({
      subscription: subscription(),
      rule: {
        interval_unit: 'month',
        interval_count: 1,
        anchor_date: '2026-01-01',
        month_end: false,
      },
      as_of: '2026-11-03',
      timezone: 'Asia/Tokyo',
      candidate_date: null,
      reason,
      algorithm_version: 'calendar-v1',
      overdue: false,
    });
    assert.equal(value.reason, reason);
    assert.equal(value.candidate_date, null);
  }
});
