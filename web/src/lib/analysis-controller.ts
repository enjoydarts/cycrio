import { ApiError, type TokenProvider } from './api.ts';
import type {
  AnalysisAPI,
  AnalysisResult,
  AnalysisPage,
} from './analysis-api.ts';
import type { Subscription } from './subscriptions-api.ts';
import { createFeed, type FeedView } from './daily-controller.ts';
export function adoptFreshAnalysisSelection(
  selected: AnalysisResult | null,
  update: FeedView<AnalysisPage | AnalysisResult>,
): AnalysisResult | null {
  if (update.status === 'denied' || update.status === 'disposed') return null;
  if (!selected || update.status !== 'ready' || update.error || !update.page)
    return selected;
  const candidates =
    'results' in update.page ? update.page.results : [update.page];
  return (
    candidates.find(
      (r) => r.id === selected.id && r.account_id === selected.account_id,
    ) ?? selected
  );
}
export function createAnalysisController(
  api: AnalysisAPI,
  token: TokenProvider,
  changed: (v: FeedView<Awaited<ReturnType<AnalysisAPI['results']>>>) => void,
  onDenied: () => void,
) {
  return createFeed(api.results, token, changed, onDenied);
}
export function createAnalysisHistory(
  api: AnalysisAPI,
  token: TokenProvider,
  changed: (v: FeedView<Awaited<ReturnType<AnalysisAPI['runs']>>>) => void,
  onDenied: () => void,
) {
  return createFeed(api.runs, token, changed, onDenied);
}
export function createAnalysisDetailController(
  api: AnalysisAPI,
  token: TokenProvider,
  changed: (v: FeedView<AnalysisResult>) => void,
  onDenied: () => void,
) {
  return createFeed(
    async (
      target: { id: string; account_id: string; fallback?: AnalysisResult },
      bearer: TokenProvider,
      signal?: AbortSignal,
    ) => {
      const page = await api.results(
        {
          account_id: target.account_id,
          state: 'all',
          review: 'all',
          limit: 100,
          offset: 0,
        },
        bearer,
        signal,
      );
      const found = page.results.find((r) => r.id === target.id);
      if (found) return found;
      if (target.fallback) return target.fallback;
      throw new ApiError('not_found');
    },
    token,
    changed,
    onDenied,
  );
}
export function acknowledgeAnalysisRegistration(
  result: AnalysisResult,
  subscription: Subscription,
  index: number,
): AnalysisResult | null {
  if (
    !result.events[index] ||
    !subscription.sources.some(
      (s) => s.result_id === result.id && s.event_index === index,
    )
  )
    return null;
  const registrations = [
    ...result.registrations.filter((r) => r.event_index !== index),
    { event_index: index, subscription_id: subscription.id },
  ];
  const automation = [
    ...(result.automation ?? []).filter((a) => a.event_index !== index),
    {
      event_index: index,
      state: 'registered' as const,
      reason: 'already_registered' as const,
      subscription_id: subscription.id,
    },
  ].sort((a, b) => a.event_index - b.event_index);
  const unresolved = automation.filter(
    (a) => !registrations.some((r) => r.event_index === a.event_index),
  );
  const automation_state: AnalysisResult['automation_state'] =
    registrations.length === result.events.length
      ? 'registered'
      : unresolved.some((a) => a.state === 'failed')
        ? 'failed'
        : unresolved.some((a) => a.state === 'needs_review')
          ? 'needs_review'
          : result.automation_state === 'registered'
            ? 'pending'
            : result.automation_state;
  return { ...result, registrations, automation, automation_state };
}
