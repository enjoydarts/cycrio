import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import {
  processingIDs as ids,
  processingPreview,
} from './mail-processing-fixtures.ts';
const load = async () => {
  const module = await import('./mail-automation-api.ts').catch(() => null);
  assert.ok(module, 'automatic mail API is implemented');
  const controller = await import('./mail-automation-controller.ts').catch(
    () => null,
  );
  assert.ok(controller, 'automatic mail controller is implemented');
  return { ...module, ...controller };
};
export const automationFixture = () => ({
  id: '',
  account_id: ids.account,
  version: 0,
  generation: 0,
  state: 'not_configured',
  reason: '',
  ready: true,
  preparation_ready: true,
  preparation_blocked_reasons: [],
  blocked_reasons: [],
  scope: null,
  default_scope: {
    since: '2023-02-28',
    before: '2024-03-01',
    timezone: 'Asia/Tokyo',
    full_email: true,
    interval_seconds: 300,
    auto_register: true,
  },
  basis: {
    account_version: 1,
    runtime_version: 2,
    profile_id: ids.profile,
    policy_version: 3,
  },
  progress: {
    bootstrap_complete: false,
    historical_complete: false,
    discovered_count: 0,
    pending_count: 0,
    completed_count: 0,
    failed_count: 0,
    unknown_count: 0,
  },
  last_success_at: null,
  next_check_at: null,
  recent_sync_runs: [],
  discovery_failure_count: 0,
  retry_at: null,
});
test('automation dates use inclusive visible end and exclusive API end across leap day', async () => {
  const { inclusiveEnd, exclusiveEnd, validAutomationRange } = await load();
  assert.equal(inclusiveEnd('2024-03-01'), '2024-02-29');
  assert.equal(exclusiveEnd('2024-02-29'), '2024-03-01');
  assert.equal(validAutomationRange('2023-02-28', '2024-02-29'), true);
  assert.equal(validAutomationRange('2024-02-30', '2024-03-01'), false);
});
test('parser accepts unprepared empty identity and cursor discovery without dates', async () => {
  const { parseMailAutomationState } = await load();
  const state = automationFixture();
  assert.equal(
    parseMailAutomationState(
      {
        ...state,
        basis: { ...state.basis, profile_id: '' },
        ready: false,
        preparation_ready: false,
      },
      ids.account,
    ).basis.profile_id,
    '',
  );
  assert.equal(parseMailAutomationState(state, ids.account).scope, null);
});
test('double click sends one request and uncertain retry keeps exact nonce/body', async () => {
  const { createMailAutomationController } = await load();
  const sent: unknown[] = [];
  let attempts = 0;
  const state = automationFixture();
  const c = createMailAutomationController(
    {
      read: async () => state,
      preferences: async () => ({ enabled: false, version: 0 }),
      save: async (_id: unknown, body: unknown) => {
        sent.push(body);
        attempts++;
        throw new ApiError('dependency_unavailable');
      },
    } as never,
    {
      preview: async () => ({
        ...processingPreview(),
        full_email_allowed: true,
      }),
    },
    async () => 't',
    () => {},
    () => {},
    () => ids.request,
  );
  await c.select(ids.account);
  c.confirm(true);
  await Promise.all([c.start(), c.start()]);
  assert.equal(attempts, 1);
  assert.equal((sent[0] as { auto_register: boolean }).auto_register, false);
  assert.equal(c.state().uncertain, true);
  c.edit({ since: '2024-01-01' });
  await c.replay();
  assert.deepEqual(sent[0], sent[1]);
  c.dispose();
});
test('old account and pre-stop polls cannot overwrite current selection or an acknowledged stop', async () => {
  const { createMailAutomationController } = await load();
  const other = ids.profile;
  let calls = 0,
    finish: ((value: unknown) => void) | undefined;
  const api = {
    read: async (id: string) => {
      calls++;
      if (calls === 1) return new Promise((resolve) => (finish = resolve));
      return {
        ...automationFixture(),
        account_id: id,
        id: ids.run,
        version: calls > 3 ? 2 : 1,
        state: calls > 3 ? 'stopped' : 'enabled',
      };
    },
    preferences: async () => ({ enabled: true, version: 1 }),
    stop: async () => ({
      ...automationFixture(),
      account_id: other,
      id: ids.run,
      version: 2,
      state: 'stopped',
    }),
  };
  const c = createMailAutomationController(
    api as never,
    {
      preview: async (id) => ({
        ...processingPreview(),
        account_id: id,
        full_email_allowed: true,
      }),
    },
    async () => 't',
    () => {},
    () => {},
    () => ids.request,
  );
  const old = c.select(ids.account);
  await c.select(other);
  finish?.(automationFixture());
  await old;
  assert.equal(c.state().snapshot?.account_id, other);
  api.read = async () => new Promise((resolve) => (finish = resolve));
  const poll = c.refresh();
  api.read = async () => ({
    ...automationFixture(),
    account_id: other,
    id: ids.run,
    version: 2,
    state: 'stopped',
  });
  await c.stop();
  finish?.({
    ...automationFixture(),
    account_id: other,
    id: ids.run,
    version: 1,
    state: 'enabled',
  });
  await poll;
  assert.equal(c.state().snapshot?.state, 'stopped');
  assert.equal(c.state().acknowledged, 'stop');
  c.dispose();
});
test('fresh preparation permits consent despite an old scope or a busy manual preview', async () => {
  const { createMailAutomationController } = await load();
  let calls = 0;
  const state = {
    ...automationFixture(),
    id: ids.run,
    version: 1,
    state: 'needs_confirmation',
    ready: false,
    preparation_ready: true,
    reason: 'uid_validity_changed',
  };
  const c = createMailAutomationController(
    {
      read: async () => state,
      preferences: async () => ({ enabled: false, version: 4 }),
      save: async () => {
        calls++;
        return { ...state, state: 'enabled', version: 2 };
      },
    } as never,
    {
      preview: async () => ({
        ...processingPreview(),
        ready: false,
        blocked_reason: 'processing_active',
        full_email_allowed: true,
      }),
    },
    async () => 't',
    () => {},
    () => {},
    () => ids.request,
  );
  await c.select(ids.account);
  assert.equal(c.state().preferences?.enabled, false);
  c.confirm(true);
  await c.start();
  assert.equal(calls, 1);
  c.dispose();
});
test('a denial purges immediately even if a sibling preview read has not settled', async () => {
  const { createMailAutomationController } = await load();
  let deny = false,
    finish: ((value: unknown) => void) | undefined;
  const c = createMailAutomationController(
    {
      read: async () => {
        if (deny) throw new ApiError('access_denied');
        return automationFixture();
      },
      preferences: async () => ({ enabled: false, version: 0 }),
    } as never,
    {
      preview: async () =>
        deny
          ? new Promise((resolve) => (finish = resolve))
          : { ...processingPreview(), full_email_allowed: true },
    } as never,
    async () => 't',
    () => {},
    () => {},
  );
  await c.select(ids.account);
  deny = true;
  const read = c.refresh();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(c.state().snapshot, null);
  assert.equal(c.state().draft, null);
  assert.equal(c.state().denied, true);
  finish?.({ ...processingPreview(), full_email_allowed: true });
  await read;
  assert.equal(c.state().snapshot, null);
  c.dispose();
});
test('409 retains custom dates and original basis until explicit adoption, then polling preserves rebased dates', async () => {
  const { createMailAutomationController } = await load();
  let snapshot = automationFixture();
  const api = {
    read: async () => structuredClone(snapshot),
    preferences: async () => ({ enabled: true, version: 1 }),
    save: async () => {
      snapshot = {
        ...snapshot,
        version: 2,
        basis: { ...snapshot.basis, policy_version: 4 },
      };
      throw new ApiError('conflict');
    },
  };
  const c = createMailAutomationController(
    api as never,
    {
      preview: async () => ({
        ...processingPreview(),
        policy_version: snapshot.basis.policy_version,
        full_email_allowed: true,
      }),
    },
    async () => 't',
    () => {},
    () => {},
    () => ids.request,
  );
  await c.select(ids.account);
  c.edit({ since: '2023-04-01' });
  c.confirm(true);
  await c.start();
  assert.equal(c.state().conflict, true);
  assert.equal(c.state().draft?.basis.policy_version, 3);
  assert.equal(c.state().draft?.since, '2023-04-01');
  c.adopt();
  assert.equal(c.state().draft?.basis.policy_version, 4);
  assert.equal(c.state().draft?.since, '2023-04-01');
  await c.refresh();
  assert.equal(c.state().draft?.since, '2023-04-01');
  c.dispose();
});
test('progress polling preserves dirty basis, detects conflict, and ACK survives followup read error', async () => {
  const { createMailAutomationController } = await load();
  let state = automationFixture(),
    failRead = false;
  const api = {
    read: async () => {
      if (failRead) throw new ApiError('dependency_unavailable');
      return structuredClone(state);
    },
    preferences: async () => ({ enabled: true, version: 3 }),
    save: async () => {
      failRead = true;
      return { ...state, id: ids.run, version: 1, state: 'enabled' };
    },
  };
  const c = createMailAutomationController(
    api as never,
    {
      preview: async () => ({
        ...processingPreview(),
        full_email_allowed: true,
      }),
    },
    async () => 't',
    () => {},
    () => {},
    () => ids.request,
  );
  await c.select(ids.account);
  c.edit({ since: '2023-03-01' });
  state = {
    ...state,
    version: 2,
    basis: { ...state.basis, policy_version: 4 },
  };
  await c.refresh();
  assert.equal(c.state().draft?.since, '2023-03-01');
  assert.equal(c.state().conflict, true);
  assert.equal(c.state().draft?.basis.policy_version, 3);
  state = { ...state, basis: { ...state.basis, policy_version: 3 } };
  await c.refresh();
  c.adopt();
  c.confirm(true);
  await c.start();
  assert.equal(c.state().acknowledged, 'start');
  assert.equal(c.state().snapshot?.state, 'enabled');
  assert.equal(c.state().uncertain, false);
  assert.ok(c.state().readError);
  c.dispose();
});
test('denial purges snapshot, consent and pending request; late poll cannot restore data', async () => {
  const { createMailAutomationController } = await load();
  let deny = false,
    denied = 0;
  const c = createMailAutomationController(
    {
      read: async () => {
        if (deny) throw new ApiError('access_denied');
        return automationFixture();
      },
      preferences: async () => ({ enabled: false, version: 0 }),
    } as never,
    {
      preview: async () => ({
        ...processingPreview(),
        full_email_allowed: true,
      }),
    },
    async () => 't',
    () => {},
    () => {
      denied++;
    },
    () => ids.request,
  );
  await c.select(ids.account);
  c.confirm(true);
  deny = true;
  await c.refresh();
  assert.equal(c.state().snapshot, null);
  assert.equal(c.state().draft, null);
  assert.equal(c.state().pending, null);
  assert.equal(denied, 1);
  deny = false;
  await c.refresh();
  assert.equal(c.state().snapshot, null);
});
test('background suspension during a write preserves replay and does not stay busy forever', async () => {
  const { createMailAutomationController } = await load();
  let finish: ((value: unknown) => void) | undefined;
  const requests: unknown[] = [];
  const c = createMailAutomationController(
    {
      read: async () => automationFixture(),
      preferences: async () => ({ enabled: false, version: 0 }),
      save: async (_id: unknown, body: unknown) => {
        requests.push(body);
        return new Promise((resolve) => (finish = resolve));
      },
    } as never,
    {
      preview: async () => ({
        ...processingPreview(),
        full_email_allowed: true,
      }),
    },
    async () => 't',
    () => {},
    () => {},
    () => ids.request,
  );
  await c.select(ids.account);
  c.confirm(true);
  const running = c.start();
  c.suspend();
  assert.equal(c.state().busy, false);
  assert.equal(c.state().uncertain, true);
  assert.ok(c.state().pending);
  finish?.({
    ...automationFixture(),
    id: ids.run,
    state: 'enabled',
    version: 1,
  });
  await running;
  assert.equal(c.state().acknowledged, null);
  const replay = c.replay();
  assert.deepEqual(requests[0], requests[1]);
  finish?.({
    ...automationFixture(),
    id: ids.run,
    state: 'enabled',
    version: 1,
  });
  await replay;
  c.dispose();
});
