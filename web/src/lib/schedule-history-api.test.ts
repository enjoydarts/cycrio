import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { id } from './subscriptions-fixtures.ts';
import {
  historyPreview,
  historyFilter,
  historyEvidence,
  historyRule,
} from './schedule-history-fixtures.ts';
import {
  createScheduleHistoryAPI,
  parseScheduleHistoryPreview,
  parseHistoryEvidence,
} from './schedule-history-api.ts';
test('history validates complete snapshot without inferring a calendar rule', () => {
  assert.deepEqual(
    parseScheduleHistoryPreview(historyPreview()),
    historyPreview(),
  );
  const mutations = [
    { token: '' },
    { token: 'g'.repeat(64) },
    { algorithm_version: 'unknown' },
    { filter: { ...historyFilter, start_date: '2026-02-30' } },
    { filter: { ...historyFilter, currency: 'jpy' } },
    { timezone: 'UTC' },
    { as_of: '2025-12-31' },
    { status: 'ambiguous' },
    { status: 'insufficient' },
    { reason: 'ended' },
    { counts: { ...historyPreview().counts, total: 4 } },
    { counts: { ...historyPreview().counts, sampled: 2 } },
    { counts: { ...historyPreview().counts, referenced_refunds: 1000 } },
    {
      samples: [
        { ...historyPreview().samples[0], version: 2 },
        ...historyPreview().samples.slice(1),
      ],
    },
    { samples: [...historyPreview().samples].reverse() },
    {
      entries: historyPreview().entries.map((e) => ({ ...e, state: 'voided' })),
    },
    {
      candidates: [{ ...historyPreview().candidates[0], candidate_date: null }],
    },
    {
      candidates: [
        { ...historyPreview().candidates[0], candidate_date: '2026-10-25' },
      ],
    },
  ];
  for (const patch of mutations)
    assert.throws(
      () => parseScheduleHistoryPreview({ ...historyPreview(), ...patch }),
      ApiError,
      JSON.stringify(patch),
    );
});
test('too many is explicit and duplicate dates may include otherwise valid ordered samples', () => {
  const full = {
    ...historyPreview(),
    status: 'too_many',
    token: '',
    counts: null,
    samples: [],
    entries: [],
    candidates: [],
  };
  assert.equal(parseScheduleHistoryPreview(full).status, 'too_many');
  assert.throws(() =>
    parseScheduleHistoryPreview({ ...full, counts: historyPreview().counts }),
  );
  const duplicate = historyPreview();
  duplicate.samples[1].occurred_on = duplicate.samples[0].occurred_on;
  duplicate.entries[1].occurred_on = duplicate.entries[0].occurred_on;
  assert.equal(
    parseScheduleHistoryPreview({
      ...duplicate,
      status: 'duplicate_dates',
      candidates: [],
    }).status,
    'duplicate_dates',
  );
});
test('evidence validates immutable scope and selected rule; manual legacy remains separate', () => {
  assert.deepEqual(
    parseHistoryEvidence(historyEvidence(), historyRule, '2026-10-26'),
    historyEvidence(),
  );
  for (const patch of [
    { as_of: '2026-10-25' },
    { samples: historyEvidence().samples.slice(0, 2) },
    { selected_rule: { ...historyRule, interval_count: 2 } },
    {
      samples: historyEvidence().samples.map((s) => ({
        ...s,
        currency: 'USD',
      })),
    },
    {
      samples: historyEvidence().samples.map((s) => ({
        ...s,
        occurred_on: '2026-07-31',
      })),
    },
  ])
    assert.throws(() =>
      parseHistoryEvidence(
        { ...historyEvidence(), ...patch },
        historyRule,
        '2026-10-26',
      ),
    );
});
test('explicit readonly POST sends filter only with fresh bearer, no cookies, no store and abort signal', async () => {
  const calls: RequestInit[] = [];
  let tokens = 0;
  const api = createScheduleHistoryAPI(
    'https://api.example/',
    async (url, init) => {
      assert.equal(
        String(url),
        `https://api.example/v1/subscriptions/${id}/billing-schedule/history-preview`,
      );
      calls.push(init!);
      return Response.json(historyPreview());
    },
  );
  const abort = new AbortController();
  const token = async () => `token-${++tokens}`;
  await api.preview(
    id,
    { ...historyFilter, samples: [] } as typeof historyFilter,
    token,
    abort.signal,
  );
  await api.preview(id, historyFilter, token);
  assert.deepEqual(JSON.parse(String(calls[0].body)), historyFilter);
  assert.equal(calls[0].method, 'POST');
  assert.equal(calls[0].credentials, 'omit');
  assert.equal(calls[0].cache, 'no-store');
  assert.equal(calls[0].signal, abort.signal);
  assert.equal(
    new Headers(calls[1].headers).get('Authorization'),
    'Bearer token-2',
  );
  await assert.rejects(
    api.preview(id, { ...historyFilter, currency: 'jpy' }, token),
    (e) => e instanceof ApiError && e.code === 'invalid_request',
  );
  assert.equal(tokens, 2);
  abort.abort();
  await assert.rejects(api.preview(id, historyFilter, token, abort.signal), {
    name: 'AbortError',
  });
  assert.equal(calls.length, 2);
});
test('history fails closed on mismatched scope and safe API failures', async () => {
  for (const data of [
    {
      ...historyPreview(),
      subscription: {
        ...historyPreview().subscription,
        id: 'ffffffff-ffff-4fff-8fff-ffffffffffff',
      },
    },
    { ...historyPreview(), filter: { ...historyFilter, currency: 'USD' } },
  ]) {
    await assert.rejects(
      createScheduleHistoryAPI('', async () => Response.json(data)).preview(
        id,
        historyFilter,
        async () => 'token',
      ),
      ApiError,
    );
  }
  for (const [status, code] of [
    [401, 'authentication_required'],
    [403, 'access_denied'],
    [404, 'not_found'],
    [500, 'dependency_unavailable'],
  ] as const) {
    await assert.rejects(
      createScheduleHistoryAPI('', async () =>
        Response.json({ error: 'secret' }, { status }),
      ).preview(id, historyFilter, async () => 'token'),
      (e) =>
        e instanceof ApiError &&
        e.code === code &&
        !e.message.includes('secret'),
    );
  }
});
test('history accepts full billing decimal precision without rounding', () => {
  const snapshot = historyPreview();
  const amount = '123456789012345678901234.123456789012';
  snapshot.samples[0].amount = amount;
  snapshot.entries[0].amount = amount;
  assert.equal(parseScheduleHistoryPreview(snapshot).samples[0].amount, amount);
});
test('duplicate status takes precedence over insufficient for two same-day payments', () => {
  const snapshot = historyPreview();
  const samples = snapshot.samples
    .slice(0, 2)
    .map((s) => ({ ...s, occurred_on: '2026-07-31' }));
  const entries = snapshot.entries
    .slice(0, 2)
    .map((s) => ({ ...s, occurred_on: '2026-07-31' }));
  const duplicate = {
    ...snapshot,
    status: 'duplicate_dates',
    counts: { ...snapshot.counts, total: 2, eligible: 2, sampled: 2 },
    samples,
    entries,
    candidates: [],
  };
  assert.equal(
    parseScheduleHistoryPreview(duplicate).status,
    'duplicate_dates',
  );
});
