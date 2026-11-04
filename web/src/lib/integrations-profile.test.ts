import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import {
  createIntegrationAPI,
  type IntegrationAPI,
  type IntegrationView,
} from './integrations-api.ts';
import { createIntegrationController } from './integrations-controller.ts';

const view = (ready = false, version = 1): IntegrationView => ({
  config: {
    version,
    sender: 'sender@example.com',
    clerk_instance_id: 'ins_synthetic',
  },
  profile_ready: ready,
  profile_synced_at: ready ? '2026-11-04T00:00:00Z' : null,
  resend_ready: true,
  clerk_webhook_ready: false,
  resend_webhook_ready: false,
  attempts: [],
});
const controller = (api: IntegrationAPI, denied = () => {}) =>
  createIntegrationController(
    api,
    async () => 'synthetic',
    () => {},
    denied,
  );
const forbidden = async () => {
  throw Error('email must not be sent');
};

test('profile sync API posts only the version and returns metadata without private response fields', async () => {
  const api = createIntegrationAPI('https://api.test', async (url, init) => {
    assert.equal(
      String(url),
      'https://api.test/v1/admin/integrations/profile/sync',
    );
    assert.equal(init?.method, 'POST');
    assert.equal(init?.body, '{"expected_version":3}');
    assert.equal(
      (init?.headers as Record<string, string>).Authorization,
      'Bearer synthetic',
    );
    return Response.json({
      profile_ready: true,
      profile_synced_at: '2026-11-04T00:00:00Z',
      email: 'private@example.com',
      secret: 'sk_private',
    });
  });
  const result = await api.syncProfile(
    { expected_version: 3, email: 'arbitrary@example.com' } as any,
    async () => 'synthetic',
  );
  assert.deepEqual(result, {
    profile_ready: true,
    profile_synced_at: '2026-11-04T00:00:00Z',
  });
});

test('profile sync refreshes readiness and preserves dirty input and its original version without sending', async () => {
  let remote = view();
  const calls: string[] = [];
  const api: IntegrationAPI = {
    view: async () => {
      calls.push('GET');
      return remote;
    },
    save: forbidden,
    test: forbidden,
    syncProfile: async (input) => {
      calls.push('SYNC');
      assert.equal(input.expected_version, 1);
      remote = view(true, 2);
      return {
        profile_ready: true,
        profile_synced_at: remote.profile_synced_at,
      };
    },
  };
  const c = controller(api);
  await c.load();
  c.edit({ sender: 'draft@example.com', instance: 'ins_draft' });
  await c.syncProfile();
  assert.deepEqual(calls, ['GET', 'SYNC', 'GET']);
  assert.equal(c.state.view?.profile_ready, true);
  assert.equal(c.state.sender, 'draft@example.com');
  assert.equal(c.state.instance, 'ins_draft');
  assert.equal(c.state.version, 1);
  assert.equal(c.state.dirty, true);
  assert.equal(c.state.pending, null);
});

test('profile sync blocks repeat clicks and discards late results after owner disposal', async () => {
  let release!: (value: {
    profile_ready: boolean;
    profile_synced_at: null;
  }) => void;
  let syncs = 0,
    reads = 0;
  const c = controller({
    view: async () => {
      reads++;
      return view();
    },
    save: forbidden,
    test: forbidden,
    syncProfile: async () => {
      syncs++;
      return await new Promise((resolve) => {
        release = resolve;
      });
    },
  });
  await c.load();
  const pending = c.syncProfile();
  await c.syncProfile();
  assert.equal(syncs, 1);
  c.dispose();
  release({ profile_ready: true, profile_synced_at: null });
  await pending;
  assert.equal(reads, 1);
  assert.equal(c.state.view, null);
  assert.equal(c.state.disposed, true);
});

test('profile sync preserves a result-unknown send request and never changes its nonce', async () => {
  let syncs = 0;
  const c = controller({
    view: async () => view(true),
    save: forbidden,
    test: async () => {
      throw new ApiError('dependency_unavailable');
    },
    syncProfile: async () => {
      syncs++;
      return { profile_ready: true, profile_synced_at: null };
    },
  });
  await c.load();
  await c.send(true);
  const nonce = c.state.pending?.request_id;
  await c.syncProfile();
  assert.equal(syncs, 0);
  assert.equal(c.state.pending?.request_id, nonce);
});

test('profile sync clears private state on access denial and keeps fixed errors on failure', async () => {
  let denied = 0;
  const c = controller(
    {
      view: async () => view(),
      save: forbidden,
      test: forbidden,
      syncProfile: async () => {
        throw new ApiError('access_denied');
      },
    },
    () => {
      denied++;
    },
  );
  await c.load();
  await c.syncProfile();
  assert.equal(denied, 1);
  assert.equal(c.state.disposed, true);
  assert.equal(c.state.view, null);
});

test('a synced unverified address stays unavailable if the follow-up view cannot load', async () => {
  let reads = 0;
  const c = controller({
    view: async () => {
      if (++reads > 1) throw Error('private upstream body');
      return view(true);
    },
    save: forbidden,
    test: forbidden,
    syncProfile: async () => ({
      profile_ready: false,
      profile_synced_at: '2026-11-04T00:00:00Z',
    }),
  });
  await c.load();
  await c.syncProfile();
  assert.equal(c.state.view?.profile_ready, false);
  assert.equal(c.state.error, 'dependency_unavailable');
  assert.ok(!JSON.stringify(c.state).includes('private upstream body'));
});
