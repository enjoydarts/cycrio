import assert from 'node:assert/strict';
import test from 'node:test';
import {
  reconcileStandardReview,
  createStandardReview,
} from './standard-policy-draft.ts';
import { processingPolicy } from './mail-processing-fixtures.ts';
import { routeFixture } from './ai-setup-test-fixtures.ts';
const state = () => ({
  ...processingPolicy(),
  standard_terms: {
    primary: {
      id: 'terms',
      version: '1',
      source_url: 'https://example.test/terms',
      summary: '標準規約',
      zdr: false,
      training_permitted: true,
      fixed_retention_days: null,
    },
    secondary_route: null,
    ready: false,
    blocked_reason: 'route_confirmation_required' as const,
  },
});
test('automatic initial metadata adoption keeps untouched fresh confirmations off without a conflict', () => {
  const initial = state();
  const draft = createStandardReview(initial);
  const latest = {
    ...initial,
    standard_terms: {
      ...initial.standard_terms,
      secondary_route: routeFixture(),
      ready: true,
      blocked_reason: '' as const,
    },
  };
  const next = reconcileStandardReview(draft, latest);
  assert.equal(next.conflict, false);
  assert.equal(next.draft.values.primary_confirmed, false);
  assert.equal(next.draft.values.secondary_confirmed, false);
  assert.equal(next.scope, createStandardReview(latest).scope);
});
test('same verified auth reload preserves dirty text, confirmation, and original baseline; changed terms require fresh review', () => {
  const initial = state();
  let draft = createStandardReview(initial);
  draft = {
    ...draft,
    draft: {
      ...draft.draft,
      dirty: true,
      values: {
        ...draft.draft.values,
        primary_retention: '編集中の個別契約',
        primary_confirmed: true,
        secondary_confirmed: true,
      },
    },
  };
  assert.equal(reconcileStandardReview(draft, structuredClone(initial)), draft);
  const next = reconcileStandardReview(draft, {
    ...initial,
    runtime_version: initial.runtime_version + 1,
    policy: { ...initial.policy!, version: initial.policy!.version + 1 },
  });
  assert.equal(next.conflict, true);
  assert.equal(next.draft.baseline_version, draft.draft.baseline_version);
  assert.equal(next.draft.values.primary_retention, '編集中の個別契約');
  assert.equal(next.draft.values.primary_confirmed, false);
  assert.equal(next.draft.values.secondary_confirmed, false);
});
