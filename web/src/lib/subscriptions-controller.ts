import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import { createFeed } from './daily-controller.ts';
import type { ProcessingEvent } from './mail-processing-api.ts';
import {
  parseValues,
  type Subscription,
  type SubscriptionCreate,
  type SubscriptionFilter,
  type SubscriptionValues,
  type SubscriptionsAPI,
} from './subscriptions-api.ts';
export const blankSubscription = (): SubscriptionValues => ({
  service: '',
  plan: null,
  amount: null,
  currency: null,
  interval_unit: null,
  interval_count: null,
  next_billing_date: null,
  status: 'active',
});
export const subscriptionFromEvent = (
  event: ProcessingEvent,
): SubscriptionValues => ({
  ...blankSubscription(),
  service: event.service,
  plan: event.plan,
  amount: ['refund', 'cancellation'].includes(event.event_type)
    ? null
    : event.amount,
  currency: ['refund', 'cancellation'].includes(event.event_type)
    ? null
    : event.currency,
  interval_unit: event.interval_unit,
  interval_count: event.interval_count,
  next_billing_date: event.next_billing_at,
  status: 'unknown',
});
export type EditorView = {
  values: SubscriptionValues;
  baseline: Subscription | null;
  latest: Subscription | null;
  saved: Subscription | null;
  confirmed: boolean;
  dirty: boolean;
  uncertain: boolean;
  conflict: boolean;
  status: 'ready' | 'saving' | 'error' | 'denied' | 'disposed';
  error: ErrorCode | null;
};
export type SubscriptionDrafts = Map<
  string,
  { view: EditorView; pending: SubscriptionCreate | null }
>;
export function createSubscriptionList(
  api: SubscriptionsAPI,
  token: TokenProvider,
  changed: Parameters<
    typeof createFeed<
      Awaited<ReturnType<SubscriptionsAPI['list']>>,
      SubscriptionFilter
    >
  >[2],
  onDenied: () => void,
) {
  return createFeed(api.list, token, changed, onDenied);
}
export function createSubscriptionEditor(
  api: SubscriptionsAPI,
  token: TokenProvider,
  changed: (view: EditorView) => void,
  onDenied: () => void,
  randomUUID = () => crypto.randomUUID(),
  initial: Subscription | null = null,
  source: { result_id: string; event_index: number } | null = null,
  draft?: { store: SubscriptionDrafts; key: string },
) {
  let view: EditorView = {
    values: initial ? parseValues(initial) : blankSubscription(),
    baseline: initial,
    latest: null,
    saved: null,
    confirmed: false,
    dirty: false,
    uncertain: false,
    conflict: false,
    status: 'ready',
    error: null,
  };
  const retained = draft?.store.get(draft.key);
  if (retained) view = { ...retained.view };
  let abort: AbortController | undefined,
    disposed = false,
    pending: SubscriptionCreate | null = retained?.pending ?? null;
  const publish = (patch: Partial<EditorView>) => {
    view = { ...view, ...patch };
    if (draft && !disposed) {
      if (view.saved || view.status === 'denied') draft.store.delete(draft.key);
      else draft.store.set(draft.key, { view, pending });
    }
    changed(view);
  };
  const allowed = () => !disposed && view.status !== 'denied';
  async function refreshLatest() {
    if (!allowed() || !view.baseline || view.status === 'saving') return;
    abort?.abort();
    const reading = new AbortController();
    abort = reading;
    try {
      const latest = await api.get(view.baseline.id, token, reading.signal);
      if (allowed() && abort === reading)
        publish({ latest, error: 'conflict' });
    } catch (error) {
      if (!allowed() || abort !== reading) return;
      const code =
        error instanceof ApiError ? error.code : 'dependency_unavailable';
      if (code === 'authentication_required' || code === 'access_denied') {
        pending = null;
        draft?.store.clear();
        publish({
          values: blankSubscription(),
          baseline: null,
          latest: null,
          saved: null,
          confirmed: false,
          dirty: false,
          uncertain: false,
          conflict: false,
          status: 'denied',
          error: code,
        });
        onDenied();
      } else publish({ error: code });
    }
  }
  async function submit(body: SubscriptionCreate | SubscriptionValues) {
    if (!allowed() || view.status === 'saving') return;
    abort?.abort();
    abort = new AbortController();
    publish({ status: 'saving', error: null });
    try {
      const saved = view.baseline
        ? await api.update(
            view.baseline.id,
            {
              ...body,
              expected_version: view.baseline.version,
              confirmed: true,
            },
            token,
            abort.signal,
          )
        : await api.create(body as SubscriptionCreate, token, abort.signal);
      if (!allowed()) return;
      pending = null;
      publish({
        saved,
        baseline: saved,
        values: parseValues(saved),
        status: 'ready',
        dirty: false,
        confirmed: false,
        uncertain: false,
        conflict: false,
        latest: null,
      });
    } catch (error) {
      if (!allowed()) return;
      const code =
        error instanceof ApiError ? error.code : 'dependency_unavailable';
      if (code === 'authentication_required' || code === 'access_denied') {
        pending = null;
        draft?.store.clear();
        view = {
          ...view,
          values: blankSubscription(),
          baseline: null,
          latest: null,
          saved: null,
          confirmed: false,
          uncertain: false,
          dirty: false,
          conflict: false,
          status: 'denied',
          error: code,
        };
        changed(view);
        onDenied();
        return;
      }
      if (code !== 'dependency_unavailable') pending = null;
      publish({
        status: 'error',
        error: code,
        uncertain: code === 'dependency_unavailable',
        conflict: code === 'conflict',
        confirmed: false,
      });
      if (code === 'conflict' && view.baseline) await refreshLatest();
    }
  }
  return {
    state: () => view,
    refreshLatest,
    edit(patch: Partial<SubscriptionValues>) {
      if (allowed() && view.status !== 'saving' && !view.uncertain)
        publish({
          values: { ...view.values, ...patch },
          dirty: true,
          confirmed: false,
          saved: null,
        });
    },
    confirm(confirmed: boolean) {
      if (
        allowed() &&
        view.status !== 'saving' &&
        !view.uncertain &&
        !view.conflict
      )
        publish({ confirmed });
    },
    async save() {
      if (
        !allowed() ||
        view.status === 'saving' ||
        view.uncertain ||
        view.conflict ||
        !view.confirmed
      )
        return;
      let values: SubscriptionValues;
      try {
        values = parseValues(view.values);
      } catch {
        publish({ error: 'invalid_request', status: 'error' });
        return;
      }
      if (view.baseline) await submit(values);
      else {
        pending = {
          ...values,
          request_id: randomUUID(),
          source_result_id: source?.result_id ?? null,
          source_event_index: source?.event_index ?? null,
          confirmed: true,
        };
        await submit(pending);
      }
    },
    async replay() {
      if (!view.uncertain || !allowed()) return;
      if (pending) await submit(pending);
      else if (view.baseline) await submit(view.values);
    },
    rebase() {
      if (
        allowed() &&
        view.latest &&
        view.status !== 'saving' &&
        !view.uncertain
      )
        publish({
          baseline: view.latest,
          latest: null,
          conflict: false,
          confirmed: false,
          error: null,
          status: 'ready',
        });
    },
    discard() {
      if (!allowed() || view.status === 'saving' || view.uncertain) return;
      const baseline = view.latest ?? view.baseline;
      publish({
        baseline,
        values: baseline ? parseValues(baseline) : blankSubscription(),
        latest: null,
        conflict: false,
        confirmed: false,
        dirty: false,
        saved: null,
        error: null,
        status: 'ready',
      });
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      abort?.abort();
      pending = null;
      publish({
        values: blankSubscription(),
        baseline: null,
        latest: null,
        saved: null,
        confirmed: false,
        dirty: false,
        uncertain: false,
        conflict: false,
        status: 'disposed',
        error: null,
      });
    },
  };
}
