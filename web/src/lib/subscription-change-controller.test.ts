import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import {
  createSubscriptionChange,
  validChangeSelection,
} from './subscription-change-controller.ts';
import { subscription, id } from './subscriptions-fixtures.ts';
import {
  changePreview as preview,
  changeReceipt as receipt,
} from './subscription-changes-fixtures.ts';

function setup() {
  const calls: unknown[] = [];
  let failure: 'conflict' | 'dependency_unavailable' | 'access_denied' | null =
    null;
  let done = 0,
    denied = 0,
    serial = 0;
  const api = {
    preview: async (_id: string, input: unknown) => {
      calls.push({ preview: input });
      return { ...preview(), preview_token: 'token-' + ++serial };
    },
    apply: async (_id: string, input: unknown) => {
      calls.push(input);
      if (failure) throw new ApiError(failure);
      return { receipt: receipt(), current: receipt().after };
    },
    get: async () => ({ ...subscription(), version: 4 }),
  };
  const controller = createSubscriptionChange(
    api,
    async () => 't',
    subscription(),
    { source_result_id: id, source_event_index: 0 },
    () => {},
    () => {
      done++;
    },
    () => {
      denied++;
    },
    () => id,
  );
  return {
    controller,
    calls,
    fail: (code: typeof failure) => {
      failure = code;
    },
    done: () => done,
    denied: () => denied,
  };
}

test('preview begins without selected fields and requires explicit valid selection and confirmation', async () => {
  const { controller: c, calls, done } = setup();
  await c.preview();
  assert.deepEqual(c.state().selected, []);
  await c.apply();
  assert.equal(calls.length, 1);
  c.select('amount', true);
  c.confirm(true);
  await c.apply();
  assert.equal(done(), 1);
  assert.equal(calls.length, 2);
  assert.deepEqual(
    (calls[1] as { selected_fields: string[] }).selected_fields,
    ['amount'],
  );
});

test('invalid money pairs, unchanged and nonselectable fields cannot be selected or applied', () => {
  const p = preview();
  p.subscription.amount = null;
  p.subscription.currency = null;
  assert.equal(validChangeSelection(p, ['amount']), false);
  assert.equal(validChangeSelection(p, ['amount', 'currency']), true);
  assert.equal(validChangeSelection(p, []), false);
  assert.equal(
    validChangeSelection(
      { ...p, fields: [{ ...p.fields[0], selectable: false }] },
      ['amount'],
    ),
    false,
  );
});

test('uncertain apply freezes choices and retries the identical request, token and selection', async () => {
  const s = setup(),
    c = s.controller;
  await c.preview();
  c.select('amount', true);
  c.confirm(true);
  s.fail('dependency_unavailable');
  await c.apply();
  assert.equal(c.state().uncertain, true);
  c.select('currency', true);
  c.confirm(false);
  await c.preview();
  assert.deepEqual(c.state().selected, ['amount']);
  assert.equal(c.state().confirmed, true);
  s.fail(null);
  await c.retry();
  assert.deepEqual(s.calls[1], s.calls[2]);
  assert.equal(s.done(), 1);
});

test('conflict clears choices and reloads current version before a fresh preview and confirmation', async () => {
  const s = setup(),
    c = s.controller;
  await c.preview();
  c.select('amount', true);
  c.confirm(true);
  s.fail('conflict');
  await c.apply();
  assert.equal(c.state().conflict, true);
  assert.deepEqual(c.state().selected, []);
  assert.equal(c.state().confirmed, false);
  await c.preview();
  assert.deepEqual(s.calls[2], {
    preview: {
      expected_version: 4,
      source_result_id: id,
      source_event_index: 0,
    },
  });
  assert.equal(c.state().preview?.preview_token, 'token-2');
  assert.deepEqual(c.state().selected, []);
});

test('changing target resets approval and permission loss clears source evidence', async () => {
  const s = setup(),
    c = s.controller;
  await c.preview();
  c.select('amount', true);
  c.confirm(true);
  c.reset(
    { ...subscription(), version: 2 },
    { source_result_id: id, source_event_index: 1 },
  );
  assert.equal(c.state().preview, null);
  assert.equal(c.state().confirmed, false);
  assert.deepEqual(c.state().selected, []);
  await c.preview();
  c.select('amount', true);
  c.confirm(true);
  s.fail('access_denied');
  await c.apply();
  assert.equal(s.denied(), 1);
  assert.equal(c.state().preview, null);
  assert.deepEqual(c.state().selected, []);
});

test('late preview responses are ignored after target change or disposal', async () => {
  let resolve!: (value: ReturnType<typeof preview>) => void;
  const c = createSubscriptionChange(
    {
      preview: () =>
        new Promise((r) => {
          resolve = r;
        }),
      apply: async () => ({ receipt: receipt(), current: null }),
      get: async () => subscription(),
    },
    async () => 't',
    subscription(),
    { source_result_id: id, source_event_index: 0 },
    () => {},
    () => assert.fail(),
    () => {},
    () => id,
  );
  const pending = c.preview();
  c.reset(subscription(), { source_result_id: id, source_event_index: 1 });
  resolve(preview());
  await pending;
  assert.equal(c.state().preview, null);
  c.dispose();
  assert.equal(c.state().preview, null);
});

test('an in-flight mutation cannot reset its source and unknown field reasons stay unselectable', async () => {
  let resolve!: (value: {
    receipt: ReturnType<typeof receipt>;
    current: null;
  }) => void;
  const c = createSubscriptionChange(
    {
      preview: async () => ({
        ...preview(),
        fields: [
          ...preview().fields,
          {
            key: 'plan',
            current: null,
            proposed: 'Premium',
            selectable: true,
            reason: 'new_server_rule',
          },
        ],
      }),
      apply: () =>
        new Promise((r) => {
          resolve = r;
        }),
      get: async () => subscription(),
    },
    async () => 't',
    subscription(),
    { source_result_id: id, source_event_index: 0 },
    () => {},
    () => {},
    () => {},
    () => id,
  );
  await c.preview();
  c.select('plan', true);
  assert.deepEqual(c.state().selected, []);
  c.select('amount', true);
  c.confirm(true);
  const pending = c.apply();
  c.reset(subscription(), { source_result_id: id, source_event_index: 1 });
  assert.equal(c.state().busy, true);
  assert.deepEqual(c.state().selected, ['amount']);
  resolve({ receipt: receipt(), current: null });
  await pending;
});
