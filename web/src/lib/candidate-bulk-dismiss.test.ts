import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { createCandidateBulkDismiss } from './candidate-bulk-dismiss.ts';
import type { Candidate } from './approval-api.ts';
const row = (n: number, related = 1) =>
  ({
    result_id: `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`,
    event_index: 0,
    related_count: related,
    mail_count: related,
    account_id: '00000000-0000-4000-8000-999999999999',
    account_label: '個人メール',
    event: {
      service: 'Example',
      event_type: 'renewal',
      plan: null,
      amount: null,
      currency: null,
      billed_at: null,
      period_start: null,
      period_end: null,
      next_billing_at: null,
      interval_unit: null,
      interval_count: null,
    },
  }) as Candidate;
test('bulk exclusion loads related sources and confirms before fixed bounded requests', async () => {
  const rows = Array.from({ length: 205 }, (_, i) => row(i + 1));
  const bodies: unknown[] = [];
  const c = createCandidateBulkDismiss(
    {
      related: async (_anchor, limit, offset) => ({
        candidates: rows.slice(offset, offset + limit),
        total: 205,
        limit,
        offset,
      }),
      dismiss: async (body) => {
        bodies.push(structuredClone(body));
      },
    },
    async () => null,
    () => {},
  );
  await c.prepare([row(1, 205)]);
  assert.equal(c.state().status, 'ready');
  assert.equal(c.state().total, 205);
  assert.equal(bodies.length, 0);
  await c.submit();
  assert.equal(c.state().status, 'success');
  assert.equal(c.state().completed, 205);
  assert.equal(bodies.length, 3);
  assert.equal(
    (bodies[0] as { additional_sources: unknown[] }).additional_sources.length,
    99,
  );
});
test('uncertain batch retries the same sources and preserves earlier progress', async () => {
  const rows = Array.from({ length: 101 }, (_, i) => row(i + 1));
  const bodies: unknown[] = [];
  let fail = true;
  const c = createCandidateBulkDismiss(
    {
      related: async (_a, limit, offset) => ({
        candidates: rows.slice(offset, offset + limit),
        total: 101,
        limit,
        offset,
      }),
      dismiss: async (body) => {
        bodies.push(structuredClone(body));
        if (bodies.length === 2 && fail) {
          fail = false;
          throw new ApiError('dependency_unavailable');
        }
      },
    },
    async () => null,
    () => {},
  );
  await c.prepare([row(1, 101)]);
  await c.submit();
  assert.equal(c.state().status, 'uncertain');
  assert.equal(c.state().completed, 100);
  await c.prepare([row(999)]);
  assert.equal(c.state().status, 'uncertain');
  await c.submit();
  assert.deepEqual(bodies[1], bodies[2]);
  assert.equal(c.state().completed, 101);
  assert.equal(c.state().status, 'success');
});
test('changed group and permission denial never submit partial or late prepared selections', async () => {
  let denied = 0,
    calls = 0;
  const c = createCandidateBulkDismiss(
    {
      related: async () => {
        throw new ApiError('access_denied');
      },
      dismiss: async () => {
        calls++;
      },
    },
    async () => null,
    () => {
      denied++;
    },
  );
  await c.prepare([row(1, 2)]);
  assert.equal(c.state().status, 'denied');
  assert.equal(c.state().total, 0);
  assert.equal(denied, 1);
  await c.submit();
  assert.equal(calls, 0);
  const changed = createCandidateBulkDismiss(
    {
      related: async (_a, limit, offset) => ({
        candidates: [row(1)],
        total: 2,
        limit,
        offset,
      }),
      dismiss: async () => {
        calls++;
      },
    },
    async () => null,
    () => {},
  );
  await changed.prepare([row(1, 2)]);
  assert.equal(changed.state().status, 'error');
  await changed.submit();
  assert.equal(calls, 0);
});
