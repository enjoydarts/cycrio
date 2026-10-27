import assert from 'node:assert/strict';
import test from 'node:test';
import { createIntegrationController } from './integrations-controller.ts';
import type { IntegrationAPI, IntegrationView } from './integrations-api.ts';
import { ApiError } from './api.ts';

const snapshot = (): IntegrationView => ({
  config: {
    version: 1,
    sender: 'sender@example.com',
    clerk_instance_id: 'ins_test',
  },
  profile_ready: true,
  profile_synced_at: '2026-10-27T00:00:00Z',
  resend_ready: true,
  clerk_webhook_ready: true,
  resend_webhook_ready: true,
  attempts: [],
});

test('settings conflicts retain the draft until the administrator explicitly adopts the latest basis', async () => {
  let remote = snapshot();
  const versions: number[] = [];
  const api: IntegrationAPI = {
    view: async () => remote,
    save: async (input) => {
      versions.push(input.expected_version);
      if (input.expected_version !== remote.config.version)
        throw new ApiError('conflict');
      return {
        version: input.expected_version + 1,
        sender: input.sender,
        clerk_instance_id: input.clerk_instance_id,
      };
    },
    test: async () => {
      throw new Error('must not send');
    },
  };
  const c = createIntegrationController(
    api,
    async () => 'token',
    () => {},
    () => {},
  );
  await c.load();
  c.edit({ sender: 'draft@example.com' });
  remote = {
    ...remote,
    config: { ...remote.config, version: 2, sender: 'other-admin@example.com' },
  };
  await c.save();
  await c.load();
  assert.equal(c.state.version, 1);
  assert.equal(c.state.sender, 'draft@example.com');
  c.adoptLatestBasis();
  assert.equal(c.state.version, 2);
  assert.equal(c.state.sender, 'draft@example.com');
  await c.save();
  assert.deepEqual(versions, [1, 2]);
  assert.equal(c.state.dirty, false);
});
test('test send requires confirmation and preserves the exact request after response loss', async () => {
  const requests: unknown[] = [];
  let fail = true;
  const api: IntegrationAPI = {
    view: async () => snapshot(),
    save: async () => snapshot().config,
    test: async (request) => {
      requests.push({ ...request });
      if (fail) throw new Error('private upstream body');
      return {
        id: '11111111-1111-4111-8111-111111111111',
        request_id: request.request_id,
        state: 'accepted',
        created_at: '2026-10-27T00:00:00Z',
        sent: false,
        delivered: false,
        bounced: false,
        complained: false,
        failed: false,
      };
    },
  };
  const c = createIntegrationController(
    api,
    async () => 'token',
    () => {},
    () => {},
  );
  await c.load();
  await c.send(false);
  assert.equal(requests.length, 0);
  await c.send(true);
  assert.equal(requests.length, 1);
  c.edit({ sender: 'draft@example.com' });
  await c.load();
  assert.equal(c.state.sender, 'draft@example.com');
  fail = false;
  await c.send(true);
  assert.deepEqual(requests[0], requests[1]);
  assert.equal(c.state.ack?.state, 'accepted');
  assert.ok(!JSON.stringify(c.state).includes('private upstream body'));
  c.dispose();
  await c.send(true);
  assert.equal(requests.length, 2);
});
