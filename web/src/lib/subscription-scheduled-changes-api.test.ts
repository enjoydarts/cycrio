import assert from 'node:assert/strict';
import test from 'node:test';
import { changePreview } from './subscription-changes-fixtures.ts';
import { id, subscription } from './subscriptions-fixtures.ts';
import {
  createScheduledChangesAPI,
  parseScheduledChange,
  parseScheduledPreview,
} from './subscription-scheduled-changes-api.ts';

export const scheduledPreview = () => ({
  ...changePreview(),
  source: {
    ...changePreview().source,
    original_event: {
      ...changePreview().source.original_event,
      effective_date: '2027-01-01',
    },
  },
  scheduled_for: '2027-01-01',
});
export const scheduledChange = () => ({
  id,
  request_id: id,
  subscription_id: id,
  version: 1,
  state: 'pending',
  effective_on: '2027-01-01',
  base_version: 1,
  preview: scheduledPreview(),
  selected_fields: ['amount', 'currency'],
  created_at: '2026-11-03T00:00:00Z',
  applied_at: null,
  reason: '',
});

test('scheduled DTO validates frozen date, IDs, state and selected facts, stripping unknown properties', () => {
  assert.equal(
    parseScheduledPreview(scheduledPreview()).scheduled_for,
    '2027-01-01',
  );
  assert.equal(
    'secret' in parseScheduledChange({ ...scheduledChange(), secret: 'x' }),
    false,
  );
  for (const patch of [
    { state: 'ready' },
    { id: '' },
    { version: 0 },
    { selected_fields: [] },
    { selected_fields: ['amount', 'amount'] },
    { selected_fields: ['service'] },
    { effective_on: '2027-02-30' },
    { effective_on: '2027-01-02' },
    { base_version: 2 },
    { reason: 'anything' },
    { applied_at: 'bad' },
  ])
    assert.throws(() =>
      parseScheduledChange({ ...scheduledChange(), ...patch }),
    );
  assert.throws(() =>
    parseScheduledPreview({
      ...scheduledPreview(),
      scheduled_for: '2026-11-03',
    }),
  );
  assert.throws(() =>
    parseScheduledPreview({
      ...scheduledPreview(),
      source: changePreview().source,
    }),
  );
});

test('scheduled API validates and sends only confirmed frozen fields and checks response identity', async () => {
  const calls: { url: string; body: unknown }[] = [];
  const api = createScheduledChangesAPI('/api', async (url, init) => {
    calls.push({
      url: String(url),
      body: init?.body ? JSON.parse(String(init.body)) : null,
    });
    if (String(url).endsWith('scheduled-change-preview'))
      return Response.json(scheduledPreview());
    if (String(url).endsWith('/cancel'))
      return Response.json({
        ...scheduledChange(),
        state: 'cancelled',
        version: 2,
      });
    if (init?.method === 'POST')
      return Response.json({
        schedule: scheduledChange(),
        current: subscription(),
      });
    return Response.json({
      schedules: [scheduledChange()],
      total: 1,
      limit: 20,
      offset: 0,
    });
  });
  const source = {
    expected_version: 1,
    source_result_id: id,
    source_event_index: 0,
  };
  const body = {
    ...source,
    request_id: id,
    preview_token: 'a'.repeat(64),
    selected_fields: ['amount', 'currency'] as ('amount' | 'currency')[],
    confirmed: true,
    effective_on: '2027-01-01',
  };
  await api.preview(id, source, async () => 't');
  await api.apply(
    id,
    { ...body, proposed: { amount: '999' } } as never,
    async () => 't',
  );
  await api.list(id, 0, async () => 't');
  await api.cancel(id, id, 1, async () => 't');
  assert.deepEqual(calls[1].body, body);
  assert.equal(
    calls[2].url,
    `/api/v1/subscriptions/${id}/scheduled-changes?limit=20&offset=0`,
  );
  assert.deepEqual(calls[3].body, { expected_version: 1, confirmed: true });
  await assert.rejects(
    api.apply(id, { ...body, confirmed: false }, async () => 't'),
  );
  assert.equal(calls.length, 4);
  const wrong = createScheduledChangesAPI('', async () =>
    Response.json({
      schedule: {
        ...scheduledChange(),
        request_id: '22222222-2222-4222-8222-222222222222',
      },
      current: null,
    }),
  );
  await assert.rejects(wrong.apply(id, body, async () => 't'));
});
