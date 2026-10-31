import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { createReanalysisController } from './reanalysis-controller.ts';
import {
  processingIDs as ids,
  processingPreview,
  processingRun,
} from './mail-processing-fixtures.ts';
import type { ProcessingReanalysisInput } from './mail-processing-api.ts';
function setup() {
  const sent: ProcessingReanalysisInput[] = [];
  let failure: string | null = null,
    denied = 0,
    preview = processingPreview();
  const api = {
    preview: async () => preview,
    reanalyze: async (_: string, input: ProcessingReanalysisInput) => {
      sent.push(input);
      if (failure) throw new ApiError(failure as 'conflict');
      return { run: processingRun(), accepted_count: 12000, skipped_count: 9 };
    },
  };
  const c = createReanalysisController(
    api,
    async () => 't',
    () => {},
    () => denied++,
    () => ids.request,
    () => new Date('2026-10-31T07:19:58Z'),
  );
  return {
    c,
    sent,
    fail: (v: string | null) => (failure = v),
    denied: () => denied,
    setPreview: (value: ReturnType<typeof processingPreview>) =>
      (preview = value),
  };
}
test('opening never triggers reanalysis; explicit confirmation pins cutoff and current preview versions', async () => {
  const { c, sent } = setup();
  await c.open(ids.account);
  await c.start();
  assert.equal(sent.length, 0);
  c.confirm(true);
  await c.start();
  assert.equal(sent.length, 1);
  assert.equal(sent[0].before, '2026-10-31T07:19:58.000Z');
  assert.equal(sent[0].full_email, true);
  assert.equal(sent[0].confirmed, true);
  assert.equal(sent[0].max_messages, 100);
  assert.equal(c.state().summary?.skipped_count, 9);
});
test('uncertain reception blocks fresh requests and replays exactly the same request', async () => {
  const { c, sent, fail } = setup();
  await c.open(ids.account);
  c.confirm(true);
  fail('dependency_unavailable');
  await c.start();
  await c.start();
  assert.equal(sent.length, 1);
  assert.equal(c.close(), false);
  fail(null);
  await c.replay();
  assert.deepEqual(sent[1], sent[0]);
  assert.equal(c.state().uncertain, false);
});
test('conflict refreshes preview and requires a fresh explicit confirmation', async () => {
  const { c, sent, fail, setPreview } = setup();
  await c.open(ids.account);
  c.confirm(true);
  fail('conflict');
  setPreview(
    processingPreview({
      expected_version: 7,
      runtime_version: 8,
      policy_version: 9,
    }),
  );
  await c.start();
  assert.equal(c.state().confirmed, false);
  fail(null);
  await c.start();
  assert.equal(sent.length, 1);
  c.confirm(true);
  await c.start();
  assert.equal(sent.length, 2);
  assert.equal(sent[1].expected_version, 7);
  assert.equal(sent[1].runtime_version, 8);
  assert.equal(sent[1].policy_version, 9);
  assert.equal(sent[1].before, sent[0].before);
});
test('denial and disposal prevent further work and discard private state', async () => {
  const { c, sent, fail, denied } = setup();
  await c.open(ids.account);
  c.confirm(true);
  fail('access_denied');
  await c.start();
  assert.equal(denied(), 1);
  assert.equal(c.state().account, null);
  await c.open(ids.account);
  assert.equal(c.state().status, 'denied');
  c.dispose();
  await c.start();
  assert.equal(sent.length, 1);
});
test('blocked readiness and missing full-email permission prevent sending despite confirmation', async () => {
  for (const patch of [
    { ready: false, blocked_reason: 'processing_active' as const },
    { full_email_allowed: false },
  ]) {
    let calls = 0;
    const c = createReanalysisController(
      {
        preview: async () => processingPreview(patch),
        reanalyze: async () => {
          calls++;
          throw Error();
        },
      },
      async () => 't',
      () => {},
      () => {},
    );
    await c.open(ids.account);
    c.confirm(true);
    await c.start();
    assert.equal(calls, 0);
  }
});
test('closing an idle modal cancels its consent; disposal ignores a late accepted response', async () => {
  let complete: (value: {
    run: ReturnType<typeof processingRun>;
    accepted_count: number;
    skipped_count: number;
  }) => void = () => {};
  let signal: AbortSignal | undefined;
  const c = createReanalysisController(
    {
      preview: async () => processingPreview(),
      reanalyze: async (_a, _i, _t, s) => {
        signal = s;
        return new Promise((resolve) => (complete = resolve));
      },
    },
    async () => 't',
    () => {},
    () => {},
  );
  await c.open(ids.account);
  c.confirm(true);
  assert.equal(c.close(), true);
  assert.equal(c.state().confirmed, false);
  await c.open(ids.account);
  c.confirm(true);
  const sending = c.start();
  assert.equal(c.close(), false);
  c.dispose();
  assert.equal(signal?.aborted, true);
  complete({ run: processingRun(), accepted_count: 1, skipped_count: 0 });
  await sending;
  assert.equal(c.state().status, 'disposed');
  assert.equal(c.state().summary, null);
});
