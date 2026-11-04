import assert from 'node:assert/strict';
import test from 'node:test';
import { createIntegrationController } from './integrations-controller.ts';
import type {
  EmailAttempt,
  IntegrationAPI,
  IntegrationView,
} from './integrations-api.ts';

const attempt = (
  n: number,
  created_at = '2026-11-04T00:00:00Z',
): EmailAttempt => ({
  id: `${String(n).padStart(8, '0')}-1111-4111-8111-111111111111`,
  request_id: `${String(n).padStart(8, '0')}-2222-4222-8222-222222222222`,
  state: 'accepted',
  created_at,
  sent: false,
  delivered: false,
  bounced: false,
  complained: false,
  failed: false,
});
const snapshot = (attempts: EmailAttempt[] = []): IntegrationView => ({
  config: {
    version: 1,
    sender: 'sender@example.test',
    clerk_instance_id: 'ins_synthetic',
  },
  profile_ready: true,
  profile_synced_at: null,
  resend_ready: true,
  clerk_webhook_ready: false,
  resend_webhook_ready: false,
  attempts,
});
const makeController = (view: IntegrationView, send: IntegrationAPI['test']) =>
  createIntegrationController(
    {
      view: async () => view,
      save: async () => view.config,
      syncProfile: async () => ({
        profile_ready: true,
        profile_synced_at: null,
      }),
      test: send,
    },
    async () => 'synthetic',
    () => {},
    () => {},
  );

test('send acknowledgement immediately appears in history and repeated nonce replaces only its own entry', async () => {
  const prior = attempt(1, '2026-11-03T00:00:00Z');
  const initial = snapshot([prior]);
  let calls = 0;
  const c = makeController(initial, async (input) => ({
    ...attempt(2),
    request_id: input.request_id,
    delivered: ++calls > 1,
  }));
  await c.load();
  await c.send(true);
  assert.equal(c.state.view?.attempts.length, 2);
  assert.equal(c.state.view?.attempts[0].id, attempt(2).id);
  assert.deepEqual(c.state.view?.attempts[1], prior);
  assert.deepEqual(
    initial.attempts,
    [prior],
    'the loaded view snapshot must not be mutated',
  );
  const nonce = c.state.pending?.request_id;
  await c.send(true);
  assert.equal(c.state.pending?.request_id, nonce);
  assert.equal(c.state.view?.attempts.length, 2);
  assert.equal(c.state.view?.attempts[0].delivered, true);
  assert.equal(c.state.view?.attempts[0].request_id, nonce);
});

test('send history keeps the latest thirty entries in descending time order', async () => {
  const prior = Array.from({ length: 30 }, (_, i) =>
    attempt(i + 1, new Date(Date.UTC(2026, 10, 3, 0, 0, i)).toISOString()),
  ).reverse();
  const c = makeController(snapshot(prior), async (input) => ({
    ...attempt(31),
    request_id: input.request_id,
  }));
  await c.load();
  await c.send(true);
  assert.deepEqual(
    c.state.view?.attempts.map((row) => row.id),
    [attempt(31).id, ...prior.slice(0, 29).map((row) => row.id)],
  );
});

test('send history compares actual instants and uses the server ID order for equal timestamps', async () => {
  const c = makeController(
    snapshot([
      attempt(1, '2026-11-04T09:00:00+09:00'),
      attempt(3, '2026-11-04T01:00:00Z'),
    ]),
    async (input) => ({
      ...attempt(2, '2026-11-04T01:00:00Z'),
      request_id: input.request_id,
    }),
  );
  await c.load();
  await c.send(true);
  assert.deepEqual(
    c.state.view?.attempts.map((row) => row.id),
    [attempt(3).id, attempt(2).id, attempt(1).id],
  );
});

test('a send acknowledgement arriving after owner disposal cannot repopulate history', async () => {
  let release!: (value: EmailAttempt) => void;
  const c = makeController(
    snapshot(),
    async () =>
      await new Promise((resolve) => {
        release = resolve;
      }),
  );
  await c.load();
  const request = c.send(true);
  c.dispose();
  release(attempt(1));
  await request;
  assert.equal(c.state.view, null);
  assert.equal(c.state.ack, null);
  assert.equal(c.state.pending, null);
});
