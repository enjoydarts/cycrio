import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import {
  createSubscriptionChange,
  type ChangeView,
} from './subscription-change-controller.ts';
import type {
  ChangePreview,
  ChangeSource,
} from './subscription-changes-api.ts';
import type { Subscription, SubscriptionsAPI } from './subscriptions-api.ts';
import type {
  ScheduledChange,
  ScheduledChangeResult,
  ScheduledChangesAPI,
  ScheduledPreview,
} from './subscription-scheduled-changes-api.ts';

export type ScheduledChangeView = ChangeView & {
  preview: ScheduledPreview | null;
  dateConfirmed: boolean;
};
export const canSchedulePreview = (preview: ChangePreview | null) =>
  !!preview?.source.original_event.effective_date &&
  preview.source.original_event.effective_date > preview.as_of;
export function createScheduledChange(
  api: Pick<ScheduledChangesAPI, 'preview' | 'apply'> &
    Pick<SubscriptionsAPI, 'get'>,
  token: TokenProvider,
  subscription: Subscription,
  source: ChangeSource,
  changed: (view: ScheduledChangeView) => void,
  done: (result: ScheduledChangeResult) => void,
  denied: () => void,
  requestID: () => string,
) {
  let dateConfirmed = false,
    scheduledFor = '',
    view: ScheduledChangeView;
  const controller = createSubscriptionChange<ScheduledChangeResult>(
    {
      get: api.get,
      preview: async (...args) => {
        const preview = await api.preview(...args);
        scheduledFor = preview.scheduled_for;
        return preview;
      },
      apply: (id, input, token, signal) =>
        api.apply(id, { ...input, effective_on: scheduledFor }, token, signal),
    },
    token,
    subscription,
    source,
    (next) => {
      if ((!next.preview || next.error) && !next.uncertain)
        dateConfirmed = false;
      view = {
        ...next,
        preview: next.preview as ScheduledPreview | null,
        dateConfirmed,
      };
      changed(view);
    },
    done,
    denied,
    requestID,
  );
  const state = () =>
    view ?? { ...controller.state(), preview: null, dateConfirmed };
  return {
    ...controller,
    state,
    confirmDate(confirmed: boolean) {
      const next = state();
      if (
        next.busy ||
        next.uncertain ||
        next.conflict ||
        next.denied ||
        !next.preview
      )
        return;
      dateConfirmed = confirmed;
      controller.confirm(false);
    },
    confirm(confirmed: boolean) {
      controller.confirm(confirmed && dateConfirmed);
    },
    async apply() {
      if (dateConfirmed) await controller.apply();
    },
  };
}
export type CancellationView = {
  busy: boolean;
  uncertain: boolean;
  error: ErrorCode | null;
};
export function createScheduledCancellation(
  api: Pick<ScheduledChangesAPI, 'cancel'>,
  token: TokenProvider,
  schedule: ScheduledChange,
  changed: (view: CancellationView) => void,
  done: (result: ScheduledChange) => void,
  denied: () => void,
) {
  const { id, subscription_id, version } = schedule;
  let view: CancellationView = { busy: false, uncertain: false, error: null },
    disposed = false,
    completed = false,
    abort: AbortController | undefined;
  const publish = (next: CancellationView) => {
    view = next;
    if (!disposed) changed(view);
  };
  return {
    state: () => view,
    async submit() {
      if (disposed || completed || view.busy || (view.error && !view.uncertain))
        return;
      abort = new AbortController();
      publish({ ...view, busy: true, error: null });
      try {
        const result = await api.cancel(
          subscription_id,
          id,
          version,
          token,
          abort.signal,
        );
        if (disposed) return;
        completed = true;
        publish({ busy: false, uncertain: false, error: null });
        done(result);
      } catch (e) {
        if (disposed) return;
        const error = e instanceof ApiError ? e.code : 'dependency_unavailable';
        publish({
          busy: false,
          uncertain: error === 'dependency_unavailable',
          error,
        });
        if (error === 'access_denied' || error === 'authentication_required')
          denied();
      }
    },
    dispose() {
      disposed = true;
      abort?.abort();
    },
  };
}
