import assert from 'node:assert/strict';
import test from 'node:test';
import {
  reconcileStandardReview,
  createStandardReview,
} from './standard-policy-draft.ts';
import { processingPolicy } from './mail-processing-fixtures.ts';
import { routeFixture } from './ai-setup-test-fixtures.ts';
import * as consent from './standard-policy-draft.ts';
import type { PolicyState } from './mail-processing-api.ts';
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
test('whole-email scope change clears both stage confirmations and retains entered terms', async () => {
  const module = await import('./standard-policy-draft.ts');
  assert.equal(typeof module.editStandardConsent, 'function');
  const value = {
    ...createStandardReview(state()).draft.values,
    full_email_allowed: false,
    primary_retention: '個別契約',
    primary_confirmed: true,
    secondary_confirmed: true,
  };
  const next = module.editStandardConsent(value, { full_email_allowed: true });
  assert.equal(next.primary_confirmed, false);
  assert.equal(next.secondary_confirmed, false);
  assert.equal(next.primary_retention, '個別契約');
  assert.equal(next.full_email_allowed, true);
});
const directState = (): PolicyState => {
  const current = state();
  return {
    ...current,
    profile: {
      ...current.profile!,
      secondary: { ...current.profile!.secondary, provider: 'openai' },
    },
    standard_terms: {
      ...current.standard_terms,
      secondary: {
        ...current.standard_terms.primary,
        id: 'openai-standard',
        training_permitted: false,
      },
      ready: true,
      blocked_reason: '',
    },
  };
};
test('direct standard consent needs fresh checks but no retention text and preserves independent custom choices', () => {
  const current = directState();
  const review = createStandardReview(current);
  assert.equal(review.draft.values.primary_mode, 'standard');
  assert.equal(review.draft.values.secondary_mode, 'standard');
  assert.equal(review.draft.values.primary_retention, '');
  assert.equal(review.draft.values.secondary_retention, '');
  assert.equal(consent.standardConsentInput(review, current), null);
  review.draft.values = {
    ...review.draft.values,
    full_email_allowed: true,
    primary_confirmed: true,
    secondary_confirmed: true,
  };
  const input = consent.standardConsentInput(review, current)!;
  assert.equal(input.secondary_mode, 'standard');
  assert.equal(input.secondary_terms_id, 'openai-standard');
  assert.equal(input.secondary_route_id, '');
  assert.equal(input.primary_retention, '');
  assert.equal(input.secondary_retention, '');
  review.draft.values.secondary_mode = 'custom';
  assert.equal(consent.standardConsentInput(review, current), null);
  review.draft.values.secondary_retention = '個別契約の保持条件';
  assert.equal(
    consent.standardConsentInput(review, current)!.primary_mode,
    'standard',
  );
  assert.equal(
    consent.standardConsentInput(review, current)!.secondary_mode,
    'custom',
  );
  review.draft.values.primary_mode = 'custom';
  assert.equal(consent.standardConsentInput(review, current), null);
  review.draft.values.primary_retention = '一次の個別契約';
  review.draft.values.secondary_mode = 'standard';
  assert.equal(
    consent.standardConsentInput(review, current)!.secondary_mode,
    'standard',
  );
  assert.equal(
    consent.standardConsentInput({ ...review, conflict: true }, current),
    null,
  );
});
test('stage condition edits clear only that confirmation and terms updates require fresh review', () => {
  const current = directState();
  const review = createStandardReview(current);
  review.draft.values.primary_confirmed = true;
  review.draft.values.secondary_confirmed = true;
  const next = consent.editStandardConsent(review.draft.values, {
    secondary_mode: 'custom',
  });
  assert.equal(next.primary_confirmed, true);
  assert.equal(next.secondary_confirmed, false);
  const changed = structuredClone(current);
  changed.standard_terms!.secondary!.version = 'next';
  assert.equal(consent.standardConsentInput(review, changed), null);
  assert.equal(reconcileStandardReview(review, changed).conflict, true);
  assert.equal(
    reconcileStandardReview(review, changed).draft.values.primary_confirmed,
    false,
  );
});
