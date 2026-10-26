import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { subscription, id } from './subscriptions-fixtures.ts';
import {
  createScheduleAPI,
  parseScheduleRule,
  parseScheduleView,
  parseSchedulePreview,
  parseScheduleSaveResult,
  ScheduleError,
} from './schedule-api.ts';
export const rule = {
  interval_unit: 'month' as const,
  interval_count: 1,
  anchor_date: '2026-01-31',
  month_end: true,
};
export const scheduleView = () => ({
  subscription: {
    ...subscription(),
    interval_unit: 'month' as const,
    interval_count: 1,
  },
  settings: null,
  as_of: '2026-10-26',
  timezone: 'Asia/Tokyo' as const,
  next_date_source: 'manual' as const,
  overdue: false,
});
export const schedulePreview = () => ({
  subscription: scheduleView().subscription,
  rule,
  as_of: '2026-10-26',
  timezone: 'Asia/Tokyo' as const,
  candidate_date: '2026-10-31',
  reason: '' as const,
  algorithm_version: 'calendar-v1' as const,
  overdue: false,
});
export const requestID = '22222222-2222-4222-8222-222222222222';
export const saveBody = () => ({
  ...rule,
  request_id: requestID,
  expected_version: 1,
  expected_as_of: '2026-10-26',
  expected_candidate: '2026-10-31',
  confirmed: true as const,
});
export const saveResult = () => ({
  receipt: {
    request_id: requestID,
    applied_version: 2,
    rule,
    as_of: '2026-10-26',
    candidate_date: '2026-10-31',
    algorithm_version: 'calendar-v1' as const,
    created_at: '2026-10-26T00:00:00Z',
  },
  current: {
    ...scheduleView(),
    subscription: {
      ...scheduleView().subscription,
      version: 2,
      next_billing_date: '2026-10-31',
    },
  },
});
test('schedule rule validates actual Gregorian dates, original anchor, unit and bounded count without calculating candidates', () => {
  for (const anchor_date of [
    '0001-01-31',
    '0099-02-28',
    '2000-02-29',
    '9999-12-31',
  ])
    assert.equal(
      parseScheduleRule({ ...rule, anchor_date, interval_count: 10000 })
        .anchor_date,
      anchor_date,
    );
  for (const patch of [
    { anchor_date: '0000-01-01' },
    { anchor_date: '2100-02-29' },
    { anchor_date: '2026-02-30' },
    { anchor_date: '2026-01-30' },
    { interval_count: 0 },
    { interval_count: 10001 },
    { interval_count: 1.1 },
    { interval_unit: 'week' },
    { month_end: 'true' },
  ])
    assert.throws(() => parseScheduleRule({ ...rule, ...patch }), ApiError);
  assert.equal(
    parseScheduleRule({
      ...rule,
      interval_unit: 'day',
      interval_count: 28,
      month_end: false,
    }).interval_unit,
    'day',
  );
});
test('schedule DTOs accept nullable settings and candidates, reject invalid settings and candidate reasons', () => {
  assert.deepEqual(parseScheduleView(scheduleView()), scheduleView());
  assert.deepEqual(parseSchedulePreview(schedulePreview()), schedulePreview());
  for (const reason of ['ended', 'out_of_range'])
    assert.equal(
      parseSchedulePreview({
        ...schedulePreview(),
        reason,
        candidate_date: null,
      }).candidate_date,
      null,
    );
  for (const patch of [
    { reason: 'ended' },
    { candidate_date: null },
    { candidate_date: '2026-10-25' },
    { timezone: 'UTC' },
    { algorithm_version: 'calendar-v2' },
  ])
    assert.throws(() =>
      parseSchedulePreview({ ...schedulePreview(), ...patch }),
    );
  const settings = {
    rule,
    stale: false,
    confirmed_as_of: '2026-10-26',
    confirmed_candidate: '2026-10-31',
    algorithm_version: 'calendar-v1',
    confirmed_version: 1,
    confirmed_at: '2026-10-26T00:00:00Z',
    updated_at: '2026-10-26T00:00:00Z',
  };
  assert.deepEqual(
    parseScheduleView({ ...scheduleView(), settings }).settings,
    settings,
  );
  assert.throws(() =>
    parseScheduleView({
      ...scheduleView(),
      settings: { ...settings, confirmed_version: 2 },
    }),
  );
});
test('receipt allows later current edits without pretending its old candidate is current', () => {
  const result = saveResult();
  result.current.subscription.version = 3;
  result.current.subscription.next_billing_date = '2027-01-15';
  assert.equal(
    parseScheduleSaveResult(result).current.subscription.next_billing_date,
    '2027-01-15',
  );
  assert.throws(() =>
    parseScheduleSaveResult({ ...result, current: scheduleView() }),
  );
});
test('API sends only explicit rule for readonly preview and exact frozen confirmation for save; validates target and receipt', async () => {
  const calls: { url: string; init?: RequestInit }[] = [];
  const responses = [scheduleView(), schedulePreview(), saveResult()];
  const api = createScheduleAPI('https://api.example/', async (url, init) => {
    calls.push({ url: String(url), init });
    return Response.json(responses.shift());
  });
  const token = async () => 'scoped';
  await api.get(id, token);
  await api.preview(id, rule, token);
  await api.save(id, saveBody(), token);
  assert.equal(
    calls[0].url,
    `https://api.example/v1/subscriptions/${id}/billing-schedule`,
  );
  assert.equal(calls[1].init?.method, 'POST');
  assert.deepEqual(JSON.parse(String(calls[1].init?.body)), rule);
  assert.equal(calls[2].init?.method, 'PUT');
  assert.deepEqual(JSON.parse(String(calls[2].init?.body)), saveBody());
  assert.equal(
    new Headers(calls[2].init?.headers).get('Authorization'),
    'Bearer scoped',
  );
  const wrong = createScheduleAPI('', async () =>
    Response.json({
      ...schedulePreview(),
      rule: { ...rule, interval_count: 2 },
    }),
  );
  await assert.rejects(wrong.preview(id, rule, token), ApiError);
  const mismatched = createScheduleAPI('', async () =>
    Response.json({
      ...saveResult(),
      receipt: { ...saveResult().receipt, candidate_date: '2026-11-30' },
    }),
  );
  await assert.rejects(mismatched.save(id, saveBody(), token), ApiError);
});
test('API exposes only known safe conflict reasons and owner denials; input never reaches token or network', async () => {
  let calls = 0;
  const api = createScheduleAPI('', async () => {
    calls++;
    return Response.json(
      { code: 'conflict', reason: 'preview_expired' },
      { status: 409 },
    );
  });
  await assert.rejects(
    api.save(id, saveBody(), async () => 'token'),
    (e) => e instanceof ScheduleError && e.reason === 'preview_expired',
  );
  await assert.rejects(
    api.preview(id, { ...rule, interval_count: 0 }, async () => {
      throw Error('must not request token');
    }),
    (e) => e instanceof ApiError && e.code === 'invalid_request',
  );
  assert.equal(calls, 1);
  for (const status of [401, 403, 404, 503]) {
    const denied = createScheduleAPI('', async () =>
      Response.json({ reason: 'database details' }, { status }),
    );
    await assert.rejects(
      denied.get(id, async () => 'token'),
      (e) => e instanceof ApiError && !e.message.includes('database'),
    );
  }
});
