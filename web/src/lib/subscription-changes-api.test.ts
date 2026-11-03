import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createSubscriptionChangesAPI,
  parseChangePreview,
  parseChangeReceipt,
} from './subscription-changes-api.ts';
import { subscription, id } from './subscriptions-fixtures.ts';
import {
  changeEvidence as evidence,
  changePreview as preview,
  changeReceipt as receipt,
} from './subscription-changes-fixtures.ts';

test('change API parses immutable evidence, field values and receipts and rejects malformed transport', () => {
  assert.equal(parseChangePreview(preview()).fields[0].proposed, '9');
  assert.equal(
    parseChangeReceipt(receipt()).source.original_event.service,
    evidence().original_event.service,
  );
  for (const patch of [
    { preview_token: '' },
    { timezone: 'UTC' },
    { fields: [{ ...preview().fields[0], key: 'service' }] },
    { fields: [{ ...preview().fields[0], proposed: 9 }] },
    { fields: [preview().fields[0], preview().fields[0]] },
  ])
    assert.throws(() => parseChangePreview({ ...preview(), ...patch }));
  assert.throws(() => parseChangeReceipt({ ...receipt(), applied_version: 0 }));
});

test('change receipt responses must match the contract, request, source and selected changes', async () => {
  const source = {
    expected_version: 1,
    source_result_id: id,
    source_event_index: 0,
  };
  const request = {
    ...source,
    request_id: id,
    preview_token: 'a'.repeat(64),
    selected_fields: ['amount', 'currency'],
    confirmed: true,
  } as const;
  for (const patch of [
    { request_id: '22222222-2222-4222-8222-222222222222' },
    { selected_fields: ['amount'] },
    { applied_version: 4 },
    { source: { ...evidence(), event_index: 1 } },
    {
      after: { ...receipt().after, id: '22222222-2222-4222-8222-222222222222' },
    },
  ]) {
    const api = createSubscriptionChangesAPI('', async () =>
      Response.json({
        receipt: { ...receipt(), ...patch },
        current: receipt().after,
      }),
    );
    await assert.rejects(
      api.apply(
        id,
        { ...request, selected_fields: [...request.selected_fields] },
        async () => 't',
      ),
    );
  }
  const api = createSubscriptionChangesAPI('', async () =>
    Response.json({
      receipt: receipt(),
      current: { ...receipt().after, version: 1 },
    }),
  );
  await assert.rejects(
    api.apply(
      id,
      { ...request, selected_fields: [...request.selected_fields] },
      async () => 't',
    ),
  );
});

test('change requests send frozen identities and selected keys without arbitrary proposed values', async () => {
  const calls: { url: string; method: string; body: unknown }[] = [];
  const api = createSubscriptionChangesAPI('/api', async (url, init) => {
    calls.push({
      url: String(url),
      method: init?.method ?? 'GET',
      body: init?.body ? JSON.parse(String(init.body)) : null,
    });
    return Response.json(
      String(url).endsWith('change-preview')
        ? preview()
        : init?.method === 'POST'
          ? { receipt: receipt(), current: receipt().after }
          : { changes: [receipt()], total: 1, limit: 20, offset: 0 },
    );
  });
  const source = {
    expected_version: 1,
    source_result_id: id,
    source_event_index: 0,
  };
  await api.preview(id, source, async () => 't');
  await api.apply(
    id,
    {
      ...source,
      request_id: id,
      preview_token: 'a'.repeat(64),
      selected_fields: ['amount', 'currency'],
      confirmed: true,
      proposed: { amount: '999' },
    } as never,
    async () => 't',
  );
  await api.list(id, 0, async () => 't');
  assert.deepEqual(calls[0], {
    url: `/api/v1/subscriptions/${id}/change-preview`,
    method: 'POST',
    body: source,
  });
  assert.deepEqual(calls[1].body, {
    ...source,
    request_id: id,
    preview_token: 'a'.repeat(64),
    selected_fields: ['amount', 'currency'],
    confirmed: true,
  });
  assert.equal(
    calls[2].url,
    `/api/v1/subscriptions/${id}/changes?limit=20&offset=0`,
  );
  await assert.rejects(
    api.apply(
      id,
      {
        ...source,
        request_id: id,
        preview_token: 't',
        selected_fields: ['service'],
        confirmed: true,
      } as never,
      async () => 't',
    ),
  );
  assert.equal(calls.length, 3);
  await assert.rejects(
    api.preview(
      id,
      { ...source, additional_sources: [{ result_id: id, event_index: 0 }] },
      async () => 't',
    ),
  );
  assert.equal(calls.length, 3);
});
