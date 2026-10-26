import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import {
  parseScheduleRule,
  sameScheduleRule,
  ScheduleError,
  type ScheduleAPI,
  type SchedulePreview,
  type ScheduleReason,
  type ScheduleRule,
  type ScheduleSave,
  type ScheduleSaveResult,
  type ScheduleView,
} from './schedule-api.ts';
import type { Subscription } from './subscriptions-api.ts';
import {
  createScheduleHistoryController,
  initialHistoryView,
  type HistoryEditorView,
} from './schedule-history-controller.ts';
import type {
  HistoryBasis,
  HistoryFilter,
  ScheduleHistoryAPI,
} from './schedule-history-api.ts';
export type ScheduleDraft = {
  interval_unit: ScheduleRule['interval_unit'] | '';
  interval_count: string;
  anchor_date: string;
  month_end: boolean;
};
export type ScheduleEditorView = {
  history: HistoryEditorView;
  historyBasis: HistoryBasis | null;
  historyAsOf: string | null;
  historyNeedsReview: boolean;
  historyManual: boolean;
  values: ScheduleDraft;
  baseline: ScheduleView | null;
  latest: ScheduleView | null;
  preview: SchedulePreview | null;
  saved: ScheduleSaveResult | null;
  confirmed: boolean;
  dirty: boolean;
  uncertain: boolean;
  conflict: boolean;
  loading: 'view' | 'preview' | 'latest' | null;
  status: 'ready' | 'saving' | 'error' | 'denied' | 'disposed';
  error: ErrorCode | null;
  reason: ScheduleReason | null;
};
// Read-only metadata can advance independently from the draft's CAS baseline.
export function scheduleDisplaySnapshot(
  view: Pick<ScheduleEditorView, 'baseline' | 'latest' | 'saved'>,
): ScheduleView | null {
  return [
    view.baseline,
    view.saved?.current ?? null,
    view.latest,
  ].reduce<ScheduleView | null>((shown, candidate) => {
    if (!candidate) return shown;
    if (
      !shown ||
      candidate.subscription.version > shown.subscription.version ||
      (candidate.subscription.version === shown.subscription.version &&
        candidate.as_of >= shown.as_of)
    )
      return candidate;
    return shown;
  }, null);
}
type StoredDraft = { view: ScheduleEditorView; pending: ScheduleSave | null };
export type ScheduleDrafts = Map<string, StoredDraft>;
const blank = (): ScheduleDraft => ({
  interval_unit: '',
  interval_count: '',
  anchor_date: '',
  month_end: false,
});
function suggested(
  s: Subscription,
  settings: ScheduleView['settings'] = null,
): ScheduleDraft {
  return {
    interval_unit: s.interval_unit ?? '',
    interval_count: s.interval_count === null ? '' : String(s.interval_count),
    anchor_date: settings?.rule.anchor_date ?? s.next_billing_date ?? '',
    month_end:
      (s.interval_unit === 'month' || s.interval_unit === 'year') &&
      (settings?.rule.month_end ?? false),
  };
}
export function draftScheduleRule(values: ScheduleDraft): ScheduleRule {
  if (!/^\d{1,5}$/.test(values.interval_count))
    throw new ApiError('invalid_request');
  return parseScheduleRule({
    ...values,
    interval_count: Number(values.interval_count),
  });
}
export const initialScheduleView = (
  subscription?: Subscription,
): ScheduleEditorView => ({
  history: initialHistoryView(subscription?.currency ?? ''),
  historyBasis: null,
  historyAsOf: null,
  historyNeedsReview: false,
  historyManual: false,
  values: subscription ? suggested(subscription) : blank(),
  baseline: null,
  latest: null,
  preview: null,
  saved: null,
  confirmed: false,
  dirty: false,
  uncertain: false,
  conflict: false,
  loading: null,
  status: 'ready',
  error: null,
  reason: null,
});
export function createScheduleController(
  api: ScheduleAPI,
  token: TokenProvider,
  changed: (view: ScheduleEditorView) => void,
  onDenied: () => void,
  options: {
    subscription: Subscription;
    drafts: ScheduleDrafts;
    requestID?: () => string;
    historyAPI?: ScheduleHistoryAPI;
  },
) {
  const id = options.subscription.id,
    drafts = options.drafts,
    stored = drafts.get(id);
  let view = stored
    ? {
        ...stored.view,
        values: { ...stored.view.values },
        loading: null,
        status: 'ready' as const,
      }
    : initialScheduleView(options.subscription);
  let pending = stored?.pending ?? null,
    disposed = false,
    acknowledged = false,
    activeWrite = false,
    generation = 0,
    read: AbortController | null = null;
  if (pending) view = { ...view, uncertain: true };
  const allowed = () => !disposed && view.status !== 'denied';
  const frozen = () => !allowed() || activeWrite || !!pending || !!view.saved;
  function remember() {
    if (view.status !== 'denied' && !acknowledged)
      drafts.set(id, {
        view: { ...view, values: { ...view.values } },
        pending,
      });
  }
  function publish(patch: Partial<ScheduleEditorView>) {
    view = { ...view, ...patch };
    remember();
    changed(view);
  }
  function cancelRead() {
    generation++;
    read?.abort();
    read = null;
    history.cancel();
  }
  function deny(code: ErrorCode) {
    cancelRead();
    history.purge();
    pending = null;
    activeWrite = false;
    drafts.clear();
    view = { ...initialScheduleView(), status: 'denied', error: code };
    changed(view);
    onDenied();
  }
  function failure(error: unknown): ErrorCode {
    const code =
      error instanceof ApiError ? error.code : 'dependency_unavailable';
    if (code === 'authentication_required' || code === 'access_denied')
      deny(code);
    else publish({ status: 'error', error: code, loading: null });
    return code;
  }
  const history = createScheduleHistoryController({
    id,
    api: options.historyAPI,
    token,
    initial: view.history,
    frozen,
    changed: (history) => publish({ history }),
    denied: deny,
    observed(result) {
      if (!view.baseline) return;
      if (result.subscription.version !== view.baseline.subscription.version) {
        cancelRead();
        publish({
          loading: null,
          conflict: true,
          latest: null,
          preview: null,
          confirmed: false,
        });
      } else if (
        result.as_of !== view.baseline.as_of ||
        (view.preview && view.preview.as_of !== result.as_of)
      ) {
        cancelRead();
        publish({
          loading: null,
          preview: null,
          confirmed: false,
          ...(view.historyBasis && view.historyAsOf !== result.as_of
            ? {
                historyBasis: null,
                historyAsOf: null,
                historyNeedsReview: true,
              }
            : {}),
        });
      }
    },
  });
  async function fetchView(latest = false) {
    if (!allowed() || activeWrite || pending || view.saved) return;
    cancelRead();
    const own = generation,
      abort = new AbortController();
    read = abort;
    publish({ loading: latest ? 'latest' : 'view', error: null });
    const current = () =>
      allowed() && !activeWrite && generation === own && !abort.signal.aborted;
    try {
      const next = await api.get(id, token, abort.signal);
      if (!current()) return;
      if (
        latest ||
        (view.baseline &&
          next.subscription.version !== view.baseline.subscription.version)
      )
        publish({
          latest: next,
          conflict: true,
          preview: null,
          confirmed: false,
          loading: null,
          status: 'ready',
        });
      else if (!view.baseline)
        publish({
          baseline: next,
          values: view.dirty
            ? view.values
            : suggested(next.subscription, next.settings),
          loading: null,
          status: 'ready',
        });
      else
        publish({
          baseline: next,
          ...(view.baseline.as_of !== next.as_of
            ? { preview: null, confirmed: false }
            : {}),
          loading: null,
          status: 'ready',
        });
    } catch (error) {
      if (current()) failure(error);
    }
  }
  async function performWrite(body: ScheduleSave) {
    if (!allowed() || activeWrite) return;
    cancelRead();
    activeWrite = true;
    pending = body;
    publish({ status: 'saving', loading: null, error: null, reason: null });
    try {
      const saved = await api.save(id, body, token);
      if (!allowed()) return;
      pending = null;
      publish({
        status: 'ready',
        saved,
        uncertain: false,
        confirmed: false,
        preview: null,
        error: null,
        reason: null,
      });
    } catch (error) {
      if (!allowed()) return;
      const code =
        error instanceof ApiError ? error.code : 'dependency_unavailable';
      if (code === 'authentication_required' || code === 'access_denied') {
        deny(code);
        return;
      }
      if (code === 'dependency_unavailable')
        publish({ status: 'error', uncertain: true, error: code });
      else {
        pending = null;
        const reason = error instanceof ScheduleError ? error.reason : null;
        if (reason === 'history_changed') history.clear();
        publish({
          status: 'error',
          uncertain: false,
          error: code,
          reason,
          ...(reason === 'history_changed'
            ? {
                historyBasis: null,
                historyAsOf: null,
                historyNeedsReview: true,
              }
            : {}),
          preview: null,
          confirmed: false,
          conflict:
            code === 'conflict' &&
            reason !== 'preview_expired' &&
            reason !== 'preview_changed' &&
            reason !== 'history_changed' &&
            reason !== 'ended',
          latest: null,
        });
      }
    } finally {
      activeWrite = false;
    }
  }
  const controller = {
    state: () => view,
    load: () => fetchView(),
    refreshLatest: () => fetchView(true),
    toggleHistory: (expanded: boolean) => history.toggle(expanded),
    editHistory: (patch: Partial<HistoryFilter>) => history.edit(patch),
    fetchHistory: () => history.fetch(),
    adoptHistory(index: number) {
      const result = view.history.result,
        candidate = result?.candidates[index];
      if (
        frozen() ||
        view.conflict ||
        view.history.loading ||
        !view.baseline ||
        !result ||
        !candidate?.candidate_date ||
        candidate.reason ||
        result.reason ||
        result.subscription.version !== view.baseline.subscription.version
      )
        return;
      cancelRead();
      const basis: HistoryBasis = Object.freeze({
        filter: Object.freeze({ ...result.filter }),
        token: result.token,
        algorithm_version: result.algorithm_version,
      });
      publish({
        values: {
          ...candidate.rule,
          interval_count: String(candidate.rule.interval_count),
        },
        historyBasis: basis,
        historyAsOf: result.as_of,
        historyNeedsReview: false,
        historyManual: false,
        dirty: true,
        preview: null,
        confirmed: false,
        error: null,
        reason: null,
        loading: null,
        status: 'ready',
      });
    },
    edit(patch: Partial<ScheduleDraft>) {
      if (frozen()) return;
      const values = { ...view.values, ...patch };
      if (values.interval_unit !== 'month' && values.interval_unit !== 'year')
        values.month_end = false;
      if (
        Object.keys(values).every(
          (key) =>
            values[key as keyof ScheduleDraft] ===
            view.values[key as keyof ScheduleDraft],
        )
      )
        return;
      let unchangedRule = false;
      try {
        unchangedRule = sameScheduleRule(
          draftScheduleRule(values),
          draftScheduleRule(view.values),
        );
      } catch {
        /* Incomplete edits still count as rule changes. */
      }
      cancelRead();
      publish({
        values,
        historyBasis: unchangedRule ? view.historyBasis : null,
        historyAsOf: unchangedRule ? view.historyAsOf : null,
        historyNeedsReview: unchangedRule ? view.historyNeedsReview : false,
        historyManual:
          view.historyManual ||
          (!unchangedRule && (!!view.historyBasis || view.historyNeedsReview)),
        preview: null,
        confirmed: false,
        dirty: true,
        error: null,
        reason: null,
        loading: null,
        status: 'ready',
      });
    },
    confirm(value: boolean) {
      if (
        !frozen() &&
        view.preview?.candidate_date &&
        !view.conflict &&
        !view.historyNeedsReview
      )
        publish({ confirmed: value });
    },
    async preview() {
      if (frozen() || view.conflict || !view.baseline) return;
      let rule: ScheduleRule;
      try {
        rule = draftScheduleRule(view.values);
      } catch {
        publish({ error: 'invalid_request', preview: null, confirmed: false });
        return;
      }
      cancelRead();
      const own = generation,
        abort = new AbortController();
      read = abort;
      publish({
        preview: null,
        confirmed: false,
        loading: 'preview',
        error: null,
        reason: null,
      });
      const current = () =>
        allowed() &&
        !activeWrite &&
        own === generation &&
        !abort.signal.aborted;
      try {
        const next = await api.preview(id, rule, token, abort.signal);
        if (!current()) return;
        if (view.historyBasis && next.as_of !== view.historyAsOf) {
          history.clear();
          publish({
            historyBasis: null,
            historyAsOf: null,
            historyNeedsReview: true,
            preview: null,
            confirmed: false,
            loading: null,
            reason: 'history_changed',
            status: 'ready',
          });
          return;
        }
        if (next.subscription.version !== view.baseline!.subscription.version) {
          publish({
            conflict: true,
            preview: null,
            confirmed: false,
            loading: null,
            status: 'ready',
          });
          await fetchView(true);
        } else
          publish({
            baseline: {
              ...view.baseline!,
              as_of: next.as_of,
              overdue: next.overdue,
            },
            preview: next,
            loading: null,
            status: 'ready',
          });
      } catch (error) {
        if (current()) failure(error);
      }
    },
    async save() {
      if (
        frozen() ||
        view.conflict ||
        view.historyNeedsReview ||
        !view.baseline ||
        !view.preview?.candidate_date ||
        !view.confirmed
      )
        return;
      let rule: ScheduleRule;
      try {
        rule = draftScheduleRule(view.values);
      } catch {
        return;
      }
      if (
        !sameScheduleRule(rule, view.preview.rule) ||
        view.preview.subscription.version !== view.baseline.subscription.version
      )
        return;
      const body: ScheduleSave = Object.freeze({
        ...rule,
        request_id: (options.requestID ?? (() => crypto.randomUUID()))(),
        expected_version: view.baseline.subscription.version,
        expected_as_of: view.preview.as_of,
        expected_candidate: view.preview.candidate_date,
        confirmed: true,
        ...(view.historyBasis ? { history_basis: view.historyBasis } : {}),
      });
      await performWrite(body);
    },
    async replay() {
      if (allowed() && !activeWrite && pending && view.uncertain)
        await performWrite(pending);
    },
    rebase() {
      if (frozen() || !view.latest) return;
      cancelRead();
      history.clear();
      publish({
        historyNeedsReview: view.historyNeedsReview || !!view.historyBasis,
        historyBasis: null,
        historyAsOf: null,
        baseline: view.latest,
        latest: null,
        conflict: false,
        preview: null,
        confirmed: false,
        loading: null,
        error: null,
        reason: null,
        status: 'ready',
      });
    },
    discard() {
      if (frozen() || !view.latest) return;
      const latest = view.latest;
      cancelRead();
      history.clear();
      publish({
        historyBasis: null,
        historyAsOf: null,
        historyNeedsReview: false,
        historyManual: false,
        baseline: latest,
        values: suggested(latest.subscription, latest.settings),
        latest: null,
        conflict: false,
        preview: null,
        confirmed: false,
        dirty: false,
        loading: null,
        error: null,
        reason: null,
        status: 'ready',
      });
    },
    acknowledge() {
      if (!allowed() || activeWrite || !view.saved) return null;
      const current = view.saved.current;
      acknowledged = true;
      drafts.delete(id);
      return current;
    },
    dispose() {
      if (disposed) return;
      cancelRead();
      if (view.status !== 'denied') {
        view = {
          ...view,
          loading: null,
          status: 'ready',
          uncertain: view.uncertain || !!pending,
        };
        remember();
      }
      disposed = true;
      history.dispose();
      view = { ...initialScheduleView(), status: 'disposed' };
      changed(view);
    },
  };
  remember();
  return controller;
}
