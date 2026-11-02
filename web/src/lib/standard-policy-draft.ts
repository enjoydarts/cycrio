import {
  validRetention,
  type PolicyState,
  type StandardPolicyInput,
} from './mail-processing-api.ts';
import { createRevisionDraft, type RevisionDraft } from './model-catalog.ts';
export type StandardConsent = {
  full_email_allowed: boolean;
  primary_mode: 'standard' | 'custom';
  secondary_mode: 'standard' | 'custom';
  primary_retention: string;
  secondary_retention: string;
  primary_confirmed: boolean;
  secondary_confirmed: boolean;
};
export type StandardReview = {
  draft: RevisionDraft<StandardConsent>;
  scope: string;
  conflict: boolean;
};
export function editStandardConsent(
  value: StandardConsent,
  patch: Partial<StandardConsent>,
): StandardConsent {
  const scopeChanged =
    patch.full_email_allowed !== undefined &&
    patch.full_email_allowed !== value.full_email_allowed;
  return {
    ...value,
    ...patch,
    ...((patch.primary_mode !== undefined &&
      patch.primary_mode !== value.primary_mode) ||
    (patch.primary_retention !== undefined &&
      patch.primary_retention !== value.primary_retention)
      ? { primary_confirmed: false }
      : {}),
    ...((patch.secondary_mode !== undefined &&
      patch.secondary_mode !== value.secondary_mode) ||
    (patch.secondary_retention !== undefined &&
      patch.secondary_retention !== value.secondary_retention)
      ? { secondary_confirmed: false }
      : {}),
    ...(scopeChanged
      ? { primary_confirmed: false, secondary_confirmed: false }
      : {}),
  };
}
const scope = (state: PolicyState) =>
  JSON.stringify([
    state.runtime_version,
    state.profile?.id,
    state.policy?.version ?? 0,
    state.policy?.full_email_allowed ?? false,
    state.standard_terms,
  ]);
export function createStandardReview(state: PolicyState): StandardReview {
  const custom = state.policy?.standard_proof?.primary_mode === 'custom';
  const secondaryCustom =
    state.policy?.standard_proof?.secondary_mode === 'custom';
  return {
    scope: scope(state),
    conflict: false,
    draft: createRevisionDraft(state.policy?.version ?? 0, {
      full_email_allowed: state.policy?.full_email_allowed ?? false,
      primary_mode: custom ? 'custom' : 'standard',
      secondary_mode: secondaryCustom ? 'custom' : 'standard',
      primary_retention: custom ? (state.policy?.primary_retention ?? '') : '',
      secondary_retention: secondaryCustom
        ? (state.policy?.secondary_retention ?? '')
        : '',
      primary_confirmed: false,
      secondary_confirmed: false,
    }),
  };
}
export function standardConsentInput(
  review: StandardReview,
  state: PolicyState,
): StandardPolicyInput | null {
  const value = review.draft.values,
    terms = state.standard_terms;
  const route = state.profile?.secondary.provider === 'openrouter';
  if (
    !state.profile ||
    !terms ||
    review.scope !== scope(state) ||
    review.conflict ||
    !value.primary_confirmed ||
    !value.secondary_confirmed ||
    (value.primary_mode === 'custom' &&
      !validRetention(value.primary_retention)) ||
    (value.secondary_mode === 'custom'
      ? !validRetention(value.secondary_retention)
      : !terms.ready || (route ? !terms.secondary_route : !terms.secondary))
  )
    return null;
  return {
    expected_version: review.draft.baseline_version,
    runtime_version: state.runtime_version,
    profile_id: state.profile.id,
    enabled: true,
    full_email_allowed: value.full_email_allowed,
    primary_mode: value.primary_mode,
    primary_terms_id: value.primary_mode === 'standard' ? terms.primary.id : '',
    primary_retention:
      value.primary_mode === 'custom' ? value.primary_retention : '',
    primary_confirmed: true,
    secondary_mode:
      value.secondary_mode === 'custom'
        ? 'custom'
        : route
          ? 'route'
          : 'standard',
    secondary_terms_id:
      value.secondary_mode === 'standard' && !route ? terms.secondary!.id : '',
    secondary_route_id:
      value.secondary_mode === 'standard' && route
        ? terms.secondary_route!.id
        : '',
    secondary_retention:
      value.secondary_mode === 'custom' ? value.secondary_retention : '',
    secondary_confirmed: true,
  };
}
export function reconcileStandardReview(
  review: StandardReview | null,
  state: PolicyState,
): StandardReview {
  if (!review) return createStandardReview(state);
  if (review.scope === scope(state)) return review;
  const value = review.draft.values;
  if (
    !review.draft.dirty &&
    !value.primary_confirmed &&
    !value.secondary_confirmed
  )
    return createStandardReview(state);
  if (review.conflict && !value.primary_confirmed && !value.secondary_confirmed)
    return review;
  return {
    ...review,
    conflict: true,
    draft: {
      ...review.draft,
      values: {
        ...value,
        primary_confirmed: false,
        secondary_confirmed: false,
      },
    },
  };
}
