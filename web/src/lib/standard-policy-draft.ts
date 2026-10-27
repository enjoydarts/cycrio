import type { PolicyState } from './mail-processing-api.ts';
import { createRevisionDraft, type RevisionDraft } from './model-catalog.ts';
export type StandardConsent = {
  primary_mode: 'standard' | 'custom';
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
const scope = (state: PolicyState) =>
  JSON.stringify([
    state.runtime_version,
    state.profile?.id,
    state.policy?.version ?? 0,
    state.standard_terms,
  ]);
export function createStandardReview(state: PolicyState): StandardReview {
  const custom =
    state.policy?.standard_proof?.primary_mode === 'custom' ||
    state.profile?.secondary.provider !== 'openrouter';
  return {
    scope: scope(state),
    conflict: false,
    draft: createRevisionDraft(state.policy?.version ?? 0, {
      primary_mode: custom ? 'custom' : 'standard',
      primary_retention: custom ? (state.policy?.primary_retention ?? '') : '',
      secondary_retention: state.policy?.secondary_retention ?? '',
      primary_confirmed: false,
      secondary_confirmed: false,
    }),
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
