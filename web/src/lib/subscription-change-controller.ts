import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import type { Subscription, SubscriptionsAPI } from './subscriptions-api.ts';
import type {
  ChangeInput,
  ChangeKey,
  ChangePreview,
  ChangeResult,
  ChangeSource,
  SubscriptionChangesAPI,
} from './subscription-changes-api.ts';

export type ChangeView = {
  preview: ChangePreview | null;
  selected: ChangeKey[];
  confirmed: boolean;
  busy: boolean;
  uncertain: boolean;
  conflict: boolean;
  denied: boolean;
  error: ErrorCode | null;
};
export const selectableChange = (field: ChangePreview['fields'][number]) =>
  field.selectable &&
  field.reason === '' &&
  field.proposed !== null &&
  JSON.stringify(field.current) !== JSON.stringify(field.proposed);
export function validChangeSelection(
  preview: ChangePreview,
  selected: ChangeKey[],
) {
  if (
    !selected.length ||
    new Set(selected).size !== selected.length ||
    selected.some(
      (key) =>
        !preview.fields.some((f) => f.key === key && selectableChange(f)),
    )
  )
    return false;
  const value = (key: 'amount' | 'currency') =>
    selected.includes(key)
      ? preview.fields.find((f) => f.key === key)!.proposed
      : preview.subscription[key];
  return (value('amount') === null) === (value('currency') === null);
}
export function createSubscriptionChange(
  api: Pick<SubscriptionChangesAPI, 'preview' | 'apply'> &
    Pick<SubscriptionsAPI, 'get'>,
  token: TokenProvider,
  initial: Subscription,
  originalSource: ChangeSource,
  changed: (view: ChangeView) => void,
  done: (result: ChangeResult) => void,
  denied: () => void,
  requestID: () => string,
) {
  const copySource = (value: ChangeSource): ChangeSource => ({
    source_result_id: value.source_result_id,
    source_event_index: value.source_event_index,
    ...(value.additional_sources
      ? {
          additional_sources: value.additional_sources.map((ref) => ({
            result_id: ref.result_id,
            event_index: ref.event_index,
          })),
        }
      : {}),
  });
  const empty = (): ChangeView => ({
    preview: null,
    selected: [],
    confirmed: false,
    busy: false,
    uncertain: false,
    conflict: false,
    denied: false,
    error: null,
  });
  let view = empty(),
    target = initial,
    source = copySource(originalSource),
    disposed = false,
    sequence = 0,
    abort: AbortController | undefined,
    pending: ChangeInput | null = null;
  const publish = (patch: Partial<ChangeView>) => {
    if (!disposed) {
      view = { ...view, ...patch };
      changed(view);
    }
  };
  const failure = (e: unknown, applying: boolean) => {
    const error = e instanceof ApiError ? e.code : 'dependency_unavailable';
    const lost =
      error === 'access_denied' || error === 'authentication_required';
    const uncertain = applying && error === 'dependency_unavailable';
    if (!uncertain) pending = null;
    publish({
      busy: false,
      uncertain,
      conflict: error === 'conflict',
      denied: lost,
      error,
      ...(!uncertain ? { confirmed: false, selected: [] } : {}),
      ...(lost ? { preview: null } : {}),
    });
    if (lost && !disposed) denied();
  };
  const submit = async () => {
    if (!pending || disposed) return;
    const seq = ++sequence;
    abort = new AbortController();
    publish({ busy: true, error: null });
    try {
      const result = await api.apply(target.id, pending, token, abort.signal);
      if (disposed || seq !== sequence) return;
      pending = null;
      publish({
        busy: false,
        uncertain: false,
        confirmed: false,
        selected: [],
        error: null,
      });
      done(result);
    } catch (e) {
      if (!disposed && seq === sequence) failure(e, true);
    }
  };
  return {
    state: () => view,
    async preview() {
      if (disposed || view.busy || view.uncertain || view.denied) return;
      const reload = view.conflict;
      const seq = ++sequence;
      abort?.abort();
      abort = new AbortController();
      pending = null;
      publish({
        busy: true,
        preview: null,
        selected: [],
        confirmed: false,
        error: null,
      });
      try {
        if (reload) {
          const latest = await api.get(target.id, token, abort.signal);
          if (disposed || seq !== sequence) return;
          target = latest;
        }
        const next = await api.preview(
          target.id,
          { ...source, expected_version: target.version },
          token,
          abort.signal,
        );
        if (disposed || seq !== sequence) return;
        target = next.subscription;
        publish({ preview: next, busy: false, conflict: false });
      } catch (e) {
        if (!disposed && seq === sequence) failure(e, false);
      }
    },
    select(key: ChangeKey, checked: boolean) {
      if (
        disposed ||
        view.busy ||
        view.uncertain ||
        view.conflict ||
        !view.preview?.fields.some((f) => f.key === key && selectableChange(f))
      )
        return;
      publish({
        selected: checked
          ? [...new Set([...view.selected, key])]
          : view.selected.filter((k) => k !== key),
        confirmed: false,
        error: null,
      });
    },
    confirm(confirmed: boolean) {
      if (!disposed && !view.busy && !view.uncertain && !view.conflict)
        publish({ confirmed });
    },
    async apply() {
      if (
        disposed ||
        view.busy ||
        view.uncertain ||
        view.conflict ||
        !view.confirmed ||
        !view.preview ||
        !validChangeSelection(view.preview, view.selected)
      )
        return;
      pending = {
        ...copySource(source),
        expected_version: view.preview.subscription.version,
        request_id: requestID(),
        preview_token: view.preview.preview_token,
        selected_fields: [...view.selected],
        confirmed: true,
      };
      await submit();
    },
    async retry() {
      if (!disposed && !view.busy && view.uncertain && pending) await submit();
    },
    reset(next: Subscription, nextSource: ChangeSource) {
      if (disposed || view.uncertain || pending) return;
      sequence++;
      abort?.abort();
      pending = null;
      target = next;
      source = copySource(nextSource);
      view = empty();
      changed(view);
    },
    dispose() {
      disposed = true;
      sequence++;
      abort?.abort();
      pending = null;
    },
  };
}
