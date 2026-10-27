import assert from 'node:assert/strict';
import test from 'node:test';
import {
  processingResult,
  processingIDs as ids,
} from './mail-processing-fixtures.ts';
import { ApiError } from './api.ts';
import { subscription } from './subscriptions-fixtures.ts';
import type { AnalysisResult } from './analysis-api.ts';
const row = () => ({
  ...processingResult({ state: 'succeeded', events: [] }),
  account_id: ids.account,
  account_label: '受信',
  registrations: [],
  automation_state: 'no_events',
  automation: [],
  billing_automation: [],
});
test('resolved analysis metadata is refreshed from all results even after leaving attention', async () => {
  const module = await import('./analysis-controller.ts');
  assert.equal(typeof module.createAnalysisDetailController, 'function');
  const calls: unknown[] = [];
  let latest: unknown;
  const c = module.createAnalysisDetailController(
    {
      results: async (filter: unknown) => {
        calls.push(filter);
        return { results: [row()], total: 1, limit: 100, offset: 0 };
      },
    } as never,
    async () => 't',
    (v) => {
      latest = v.page;
    },
    () => {},
  );
  await c.load({ id: ids.result, account_id: ids.account });
  assert.equal((calls[0] as { review: string }).review, 'all');
  assert.equal(
    (latest as { automation_state: string }).automation_state,
    'no_events',
  );
  c.dispose();
});
test('source history resolution finds the current financial record across pages before opening a new editor', async () => {
  const module = await import('./billing-source.ts').catch(() => null);
  assert.ok(module);
  const source = { result_id: ids.result, event_index: 0 };
  let calls = 0;
  const entry = {
    id: ids.run,
    subscription_id: ids.profile,
    sources: [source],
    provenance: 'automatic',
  };
  const found = await module.findBillingSource(
    {
      list: async (_sid: unknown, filter: unknown) => {
        calls++;
        assert.equal(
          (filter as { source_result_id: string }).source_result_id,
          ids.result,
        );
        return {
          events: calls === 1 ? [] : [entry],
          total: 21,
          limit: 20,
          offset: calls === 1 ? 0 : 20,
        };
      },
    } as never,
    ids.profile,
    source,
    async () => 't',
    new AbortController().signal,
  );
  assert.equal(found?.id, ids.run);
  assert.equal(calls, 2);
});
test('detail denial purges the previous result and dispose rejects a late response', async () => {
  const module = await import('./analysis-controller.ts');
  assert.equal(typeof module.createAnalysisDetailController, 'function');
  let mode = 'ok',
    finish: ((value: unknown) => void) | undefined,
    denied = 0;
  let latest: unknown;
  const api = {
    results: async () => {
      if (mode === 'deny') throw new ApiError('access_denied');
      if (mode === 'late') return new Promise((resolve) => (finish = resolve));
      return { results: [row()], total: 1, limit: 100, offset: 0 };
    },
  };
  const c = module.createAnalysisDetailController(
    api as never,
    async () => 't',
    (v) => (latest = v.page),
    () => denied++,
  );
  await c.load({ id: ids.result, account_id: ids.account });
  mode = 'deny';
  await c.load({ id: ids.result, account_id: ids.account });
  assert.equal(latest, null);
  assert.equal(denied, 1);
  c.dispose();
  const c2 = module.createAnalysisDetailController(
    api as never,
    async () => 't',
    (v) => (latest = v.page),
    () => {},
  );
  mode = 'late';
  const read = c2.load({ id: ids.result, account_id: ids.account });
  c2.dispose();
  finish?.({ results: [row()], total: 1, limit: 100, offset: 0 });
  await read;
  assert.equal(latest, null);
});
test('registration ACK changes only a source verified contract outcome and retains billing exceptions', async () => {
  const module = await import('./analysis-controller.ts');
  assert.equal(typeof module.acknowledgeAnalysisRegistration, 'function');
  const result = {
    ...row(),
    events: [{ service: 'A' }, { service: 'B' }],
    automation_state: 'needs_review',
    automation: [
      {
        event_index: 0,
        state: 'needs_review',
        reason: 'ambiguous_identity',
        subscription_id: null,
      },
      {
        event_index: 1,
        state: 'needs_review',
        reason: 'ambiguous_identity',
        subscription_id: null,
      },
    ],
    billing_automation: [
      {
        event_index: 0,
        state: 'needs_review',
        reason: 'missing_facts',
        entry_id: null,
      },
    ],
  };
  assert.equal(
    module.acknowledgeAnalysisRegistration(result as never, subscription(), 0),
    null,
  );
  const next = module.acknowledgeAnalysisRegistration(
    result as never,
    {
      ...subscription(),
      sources: [
        { result_id: ids.result, event_index: 0, original_event: {} as never },
      ],
    },
    0,
  );
  assert.equal(next?.automation?.[0].state, 'registered');
  assert.equal(next?.automation?.[1].state, 'needs_review');
  assert.equal(next?.automation_state, 'needs_review');
  assert.deepEqual(next?.billing_automation, result.billing_automation);
});
for (const channel of ['list', 'detail'] as const)
  test(`${channel} selection retains a verified registration ACK through loading and read failure, then purges on denial`, async () => {
    const module = await import('./analysis-controller.ts');
    assert.equal(typeof module.adoptFreshAnalysisSelection, 'function');
    const old = {
      ...row(),
      automation_state: 'needs_review',
    } as AnalysisResult;
    const ack = {
      ...old,
      automation_state: 'registered',
      registrations: [{ event_index: 0, subscription_id: ids.profile }],
    } as AnalysisResult;
    let selected: AnalysisResult | null = old,
      mode = 'ok',
      calls = 0;
    const api = {
      results: async () => {
        calls++;
        if (mode === 'fail') throw new ApiError('dependency_unavailable');
        if (mode === 'deny') throw new ApiError('access_denied');
        return { results: [old], total: 1, limit: 100, offset: 0 };
      },
    };
    const apply = (
      update: Parameters<typeof module.adoptFreshAnalysisSelection>[1],
    ) => {
      selected = module.adoptFreshAnalysisSelection(selected, update);
    };
    const c =
      channel === 'list'
        ? module.createAnalysisController(
            api as never,
            async () => 't',
            apply,
            () => {},
          )
        : module.createAnalysisDetailController(
            api as never,
            async () => 't',
            apply,
            () => {},
          );
    const target =
      channel === 'list'
        ? {
            account_id: ids.account,
            state: 'all',
            review: 'all',
            limit: 100,
            offset: 0,
          }
        : { id: ids.result, account_id: ids.account };
    await c.load(target as never);
    selected = ack;
    mode = 'fail';
    const read = c.load(target as never);
    assert.equal(
      selected,
      ack,
      'loading must not replay the previous page over a confirmed ACK',
    );
    await read;
    assert.equal(selected, ack, 'read failure must retain the verified ACK');
    mode = 'deny';
    await c.load(target as never);
    assert.equal(selected, null);
    const deniedCalls = calls;
    mode = 'ok';
    await c.load(target as never);
    assert.equal(calls, deniedCalls);
    assert.equal(selected, null);
    c.dispose();
  });
test('fresh selection cannot restore a closed detail or replace a different account identity', async () => {
  const module = await import('./analysis-controller.ts');
  assert.equal(typeof module.adoptFreshAnalysisSelection, 'function');
  const result = row() as AnalysisResult;
  assert.equal(
    module.adoptFreshAnalysisSelection(null, {
      page: result,
      status: 'ready',
      error: null,
    }),
    null,
  );
  assert.equal(
    module.adoptFreshAnalysisSelection(result, {
      page: { ...result, account_id: ids.profile },
      status: 'ready',
      error: null,
    }),
    result,
  );
  assert.equal(
    module.adoptFreshAnalysisSelection(result, {
      page: result,
      status: 'disposed',
      error: null,
    }),
    null,
  );
});
for (const channel of ['list', 'detail'] as const)
  test(`${channel} ignores a superseded pre-ACK response after the post-ACK refresh fails`, async () => {
    const module = await import('./analysis-controller.ts');
    const old = {
      ...row(),
      automation_state: 'needs_review',
    } as AnalysisResult;
    const ack = {
      ...old,
      automation_state: 'registered',
      registrations: [{ event_index: 0, subscription_id: ids.profile }],
    } as AnalysisResult;
    let selected: AnalysisResult | null = old,
      mode = 'ok',
      finish: ((value: unknown) => void) | undefined;
    const page = { results: [old], total: 1, limit: 100, offset: 0 };
    const api = {
      results: async () => {
        if (mode === 'late')
          return new Promise((resolve) => (finish = resolve));
        if (mode === 'fail') throw new ApiError('dependency_unavailable');
        return page;
      },
    };
    const apply = (
      update: Parameters<typeof module.adoptFreshAnalysisSelection>[1],
    ) => {
      selected = module.adoptFreshAnalysisSelection(selected, update);
    };
    const c =
      channel === 'list'
        ? module.createAnalysisController(
            api as never,
            async () => 't',
            apply,
            () => {},
          )
        : module.createAnalysisDetailController(
            api as never,
            async () => 't',
            apply,
            () => {},
          );
    const target =
      channel === 'list'
        ? {
            account_id: ids.account,
            state: 'all',
            review: 'all',
            limit: 100,
            offset: 0,
          }
        : { id: ids.result, account_id: ids.account };
    await c.load(target as never);
    mode = 'late';
    const pending = c.load(target as never);
    selected = ack;
    mode = 'fail';
    await c.load(target as never);
    finish?.(page);
    await pending;
    assert.equal(selected, ack);
    c.dispose();
    assert.equal(selected, null);
  });
