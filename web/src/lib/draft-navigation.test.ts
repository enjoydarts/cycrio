import assert from 'node:assert/strict';
import test from 'node:test';
import { createDraftNavigation, hasDraftChanges } from './draft-navigation.ts';
test('all retained editor stores participate in dirty checks while saved receipts do not', () => {
  const subscription = [
    { view: { dirty: false, uncertain: false, status: 'ready' } },
  ];
  const schedule = [
    { view: { dirty: true, uncertain: false, status: 'ready', saved: null } },
  ];
  const billing = [
    {
      view: { dirty: false, uncertain: false, status: 'ready' },
      pending: { request_id: 'pending' },
    },
  ];
  assert.equal(hasDraftChanges(subscription, schedule), true);
  assert.equal(hasDraftChanges(subscription, billing), true);
  assert.equal(
    hasDraftChanges([
      {
        view: {
          dirty: true,
          uncertain: false,
          status: 'ready',
          saved: { receipt: 'saved' },
        },
      },
    ]),
    false,
  );
});
test('dirty navigation requires explicit discard and a newly frozen write blocks a pending confirmation', () => {
  let dirty = true,
    frozen = false,
    discarded = 0;
  const visited: string[] = [];
  const guard = createDraftNavigation(
    () => dirty,
    () => frozen,
    () => {
      discarded++;
      dirty = false;
    },
    (target) => visited.push(target),
    () => {},
  );
  assert.equal(
    guard.request('https://test/subscriptions', 'https://test/analysis'),
    false,
  );
  assert.equal(guard.target(), '/analysis');
  assert.deepEqual(visited, []);
  frozen = true;
  guard.discardAndLeave();
  assert.equal(discarded, 0);
  assert.deepEqual(visited, []);
  frozen = false;
  guard.stay();
  assert.equal(guard.target(), '');
  assert.equal(dirty, true);
  guard.request(
    'https://test/subscriptions',
    'https://test/analysis?tab=attention',
  );
  guard.discardAndLeave();
  assert.equal(discarded, 1);
  assert.deepEqual(visited, ['/analysis?tab=attention']);
  assert.equal(
    guard.request('https://test/subscriptions', 'https://test/analysis'),
    true,
  );
});
test('frozen navigation and cross-origin destinations cannot create an actionable discard target', () => {
  const guard = createDraftNavigation(
    () => true,
    () => true,
    () => assert.fail(),
    () => assert.fail(),
    () => {},
  );
  assert.equal(
    guard.request('https://test/subscriptions', 'https://evil.test/analysis'),
    false,
  );
  assert.equal(guard.target(), '');
});
