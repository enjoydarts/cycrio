import assert from 'node:assert/strict';
import test from 'node:test';
import { parseSubscription } from './subscriptions-api.ts';
import { subscriptionBillingAnchor } from './subscriptions-controller.ts';
import { subscription } from './subscriptions-fixtures.ts';
import { changeEvidence } from './subscription-changes-fixtures.ts';
test('billing anchor ignores newer evidence-only sources and missing legacy flag remains an anchor', () => {
  const source = changeEvidence();
  const saved = parseSubscription({
    ...subscription(),
    sources: [
      { ...source, received_at: '2026-10-01T00:00:00Z' },
      {
        ...source,
        event_index: 1,
        received_at: '2026-11-03T00:00:00Z',
        affects_billing_anchor: false,
      },
    ],
  });
  assert.equal(saved.sources[0].affects_billing_anchor, true);
  assert.equal(saved.sources[1].affects_billing_anchor, false);
  assert.equal(subscriptionBillingAnchor(saved), '2026-10-01T00:00:00Z');
  assert.equal(
    subscriptionBillingAnchor({ ...saved, sources: [saved.sources[1]] }),
    null,
  );
  assert.throws(() =>
    parseSubscription({
      ...subscription(),
      sources: [{ ...source, affects_billing_anchor: 'false' }],
    }),
  );
});
test('billing anchor compares timestamps chronologically across UTC offsets', () => {
  const source = changeEvidence();
  const s = parseSubscription({
    ...subscription(),
    sources: [
      { ...source, received_at: '2026-11-03T01:00:00+09:00' },
      { ...source, event_index: 1, received_at: '2026-11-02T20:00:00Z' },
    ],
  });
  assert.equal(subscriptionBillingAnchor(s), '2026-11-02T20:00:00Z');
});
