import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import { createFeed } from './daily-controller.ts';
import {
  BillingError,
  parseBillingValues,
  type BillingAPI,
  type BillingCreate,
  type BillingEntry,
  type BillingKind,
  type BillingPage,
  type BillingReason,
  type BillingSourceInput,
  type BillingUpdate,
  type BillingValues,
  type DuplicatePage,
} from './billing-api.ts';
import type { BillingSummary } from './billing-api.ts';
import type {
  Subscription,
  SubscriptionPage,
  SubscriptionsAPI,
} from './subscriptions-api.ts';
import type { ProcessingEvent } from './mail-processing-api.ts';
export const createBillingFeed = createFeed;
export function currentBillingMonth(now = new Date()): string {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Tokyo',
    year: 'numeric',
    month: '2-digit',
  }).formatToParts(now);
  return `${parts.find((p) => p.type === 'year')!.value}-${parts.find((p) => p.type === 'month')!.value}`;
}
export type SummaryFilter = { month: string; subscription_id: string | null };
export type BillingSummaryView = {
  summary: BillingSummary | null;
  selected: Subscription | null;
  contracts: SubscriptionPage | null;
  query: string;
  status: 'idle' | 'loading' | 'ready' | 'error' | 'denied' | 'disposed';
  error: ErrorCode | null;
  pickerLoading: boolean;
  pickerError: ErrorCode | null;
};
export const initialBillingSummaryView = (): BillingSummaryView => ({
  summary: null,
  selected: null,
  contracts: null,
  query: '',
  status: 'idle',
  error: null,
  pickerLoading: false,
  pickerError: null,
});
export function createBillingSummaryController(
  api: BillingAPI,
  subscriptions: SubscriptionsAPI,
  token: TokenProvider,
  changed: (view: BillingSummaryView) => void,
  onDenied: () => void,
) {
  let view = initialBillingSummaryView(),
    disposed = false,
    main: AbortController | undefined,
    picker: AbortController | undefined;
  const allowed = () => !disposed && view.status !== 'denied';
  const publish = (patch: Partial<BillingSummaryView>) => {
    view = { ...view, ...patch };
    changed(view);
  };
  function failure(error: unknown, channel: 'main' | 'picker') {
    const code =
      error instanceof ApiError ? error.code : 'dependency_unavailable';
    if (code === 'authentication_required' || code === 'access_denied') {
      main?.abort();
      picker?.abort();
      view = { ...initialBillingSummaryView(), status: 'denied', error: code };
      changed(view);
      onDenied();
    } else
      publish(
        channel === 'main'
          ? { status: 'error', error: code }
          : { pickerLoading: false, pickerError: code },
      );
  }
  return {
    state: () => view,
    async load(filter: SummaryFilter) {
      if (!allowed()) return;
      main?.abort();
      const abort = new AbortController();
      main = abort;
      publish({
        status: 'loading',
        error: null,
        summary: null,
        selected: null,
      });
      const current = () =>
        allowed() && main === abort && !abort.signal.aborted;
      async function observe<T>(read: () => Promise<T>): Promise<T> {
        try {
          if (!current()) throw new DOMException('Aborted', 'AbortError');
          return await read();
        } catch (error) {
          if (current()) failure(error, 'main');
          throw error;
        }
      }
      try {
        const [summary, selected] = await Promise.all([
          observe(() =>
            api.summary(
              filter.month,
              filter.subscription_id,
              token,
              abort.signal,
            ),
          ),
          observe(() =>
            filter.subscription_id
              ? subscriptions.get(filter.subscription_id, token, abort.signal)
              : Promise.resolve(null),
          ),
        ]);
        if (current()) publish({ summary, selected, status: 'ready' });
      } catch {
        // Each current read observes authorization failures independently,
        // including a refusal that arrives after its sibling's 503.
      }
    },
    async search(query: string, offset = 0) {
      if (!allowed()) return;
      picker?.abort();
      const abort = new AbortController();
      picker = abort;
      publish({ query, pickerLoading: true, pickerError: null });
      try {
        const contracts = await subscriptions.list(
          { q: query, status: 'all', sort: 'service', limit: 20, offset },
          token,
          abort.signal,
        );
        if (allowed() && picker === abort)
          publish({ contracts, pickerLoading: false });
      } catch (error) {
        if (allowed() && picker === abort && !abort.signal.aborted)
          failure(error, 'picker');
      }
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      main?.abort();
      picker?.abort();
      view = { ...initialBillingSummaryView(), status: 'disposed' };
      changed(view);
    },
  };
}
export type BillingDraft = Omit<BillingValues, 'kind'> & {
  kind: BillingKind | '';
};
export const blankBilling = (): BillingDraft => ({
  kind: '',
  occurred_on: '',
  amount: '',
  currency: '',
  refund_of_id: null,
  state: 'active',
});
export type SourceReference = { result_id: string; event_index: number };
export type BillingEditorView = {
  values: BillingDraft;
  baseline: BillingEntry | null;
  latest: BillingEntry | null;
  saved: BillingEntry | null;
  linked: BillingEntry | null;
  confirmed: boolean;
  duplicateConfirmed: boolean;
  dirty: boolean;
  uncertain: boolean;
  conflict: boolean;
  duplicates: DuplicatePage | null;
  payments: BillingPage | null;
  records: BillingPage | null;
  selectedPayment: BillingEntry | null;
  loading: string[];
  status: 'ready' | 'saving' | 'error' | 'denied' | 'disposed';
  error: ErrorCode | null;
  reason: BillingReason | null;
};
type Receipt =
  | { type: 'create'; body: BillingCreate }
  | { type: 'update'; id: string; body: BillingUpdate }
  | {
      type: 'attach' | 'detach';
      id: string;
      body: BillingSourceInput;
      keepDraft?: boolean;
    };
export type BillingDrafts = Map<
  string,
  {
    subscription_id: string;
    view: BillingEditorView;
    pending: Receipt | null;
    previewKey: string | null;
  }
>;
export function initialBillingView(
  initial: BillingEntry | null = null,
): BillingEditorView {
  return {
    values: initial ? parseBillingValues(initial) : blankBilling(),
    baseline: initial,
    latest: null,
    saved: null,
    linked: null,
    confirmed: false,
    duplicateConfirmed: false,
    dirty: false,
    uncertain: false,
    conflict: false,
    duplicates: null,
    payments: null,
    records: null,
    selectedPayment: null,
    loading: [],
    status: 'ready',
    error: null,
    reason: null,
  };
}
export function createBillingEditor(
  api: BillingAPI,
  token: TokenProvider,
  changed: (view: BillingEditorView) => void,
  onDenied: () => void,
  options: {
    subscription_id: string;
    initial?: BillingEntry | null;
    source?: SourceReference | null;
    drafts?: BillingDrafts;
    draftKey?: string;
    randomUUID?: () => string;
  },
) {
  const sid = options.subscription_id,
    store = options.drafts,
    key = options.draftKey;
  let source = options.source ?? null;
  const validInitial =
    options.initial?.subscription_id === sid ? options.initial : null;
  const cached = key ? store?.get(key) : undefined;
  const retained =
    cached?.subscription_id === sid &&
    (!options.initial || cached.view.baseline?.id === options.initial.id)
      ? cached
      : undefined;
  let view =
    retained?.subscription_id === sid
      ? { ...retained.view, loading: [] }
      : initialBillingView(validInitial);
  let pending: Receipt | null =
      retained?.subscription_id === sid ? retained.pending : null,
    previewKey: string | null =
      retained?.subscription_id === sid ? retained.previewKey : null;
  let disposed = false,
    writeAbort: AbortController | undefined,
    readGeneration = 0;
  const reads = new Map<string, AbortController>();
  const invalidTarget =
    !!options.initial && options.initial.subscription_id !== sid;
  const allowed = () => !disposed && !invalidTarget && view.status !== 'denied';
  const frozen = () =>
    !allowed() || !!writeAbort || view.status === 'saving' || view.uncertain;
  const currentWrite = (abort: AbortController) =>
    allowed() && writeAbort === abort && !abort.signal.aborted;
  const cancelReads = () => {
    readGeneration++;
    for (const read of reads.values()) read.abort();
    reads.clear();
  };
  const remember = () => {
    if (store && key && !disposed) {
      if (view.saved || view.status === 'denied') store.delete(key);
      else store.set(key, { subscription_id: sid, view, pending, previewKey });
    }
  };
  const publish = (patch: Partial<BillingEditorView>) => {
    view = { ...view, ...patch };
    remember();
    changed(view);
  };
  function denied(code: ErrorCode) {
    if (!allowed()) return;
    writeAbort?.abort();
    writeAbort = undefined;
    cancelReads();
    pending = null;
    previewKey = null;
    source = null;
    store?.clear();
    view = { ...initialBillingView(), status: 'denied', error: code };
    changed(view);
    onDenied();
  }
  const errorCode = (error: unknown): ErrorCode =>
    error instanceof ApiError ? error.code : 'dependency_unavailable';
  function failed(error: unknown) {
    const code = errorCode(error);
    if (code === 'authentication_required' || code === 'access_denied')
      denied(code);
    else
      publish({
        error: code,
        status: 'error',
        reason: error instanceof BillingError ? error.reason : null,
      });
  }
  async function runWrite(task: (abort: AbortController) => Promise<void>) {
    if (!allowed() || writeAbort || view.status === 'saving') return;
    const abort = new AbortController();
    writeAbort = abort;
    cancelReads();
    publish({ status: 'saving', loading: [], error: null, reason: null });
    try {
      await task(abort);
    } catch (error) {
      if (currentWrite(abort)) {
        writeAbort = undefined;
        publish({ confirmed: false });
        failed(error);
      }
    } finally {
      if (writeAbort === abort) writeAbort = undefined;
    }
  }
  async function read<T>(
    channel: string,
    task: (signal: AbortSignal) => Promise<T>,
    accept: (value: T) => void,
  ) {
    if (!allowed() || writeAbort || view.status === 'saving') return;
    reads.get(channel)?.abort();
    const abort = new AbortController();
    const generation = readGeneration;
    reads.set(channel, abort);
    const current = () =>
      allowed() &&
      !writeAbort &&
      generation === readGeneration &&
      reads.get(channel) === abort &&
      !abort.signal.aborted;
    publish({ loading: [...new Set([...view.loading, channel])], error: null });
    try {
      const result = await task(abort.signal);
      if (current()) accept(result);
    } catch (error) {
      if (current()) failed(error);
    } finally {
      if (current()) {
        reads.delete(channel);
        publish({ loading: view.loading.filter((v) => v !== channel) });
      }
    }
  }
  const duplicateKey = () =>
    JSON.stringify([
      sid,
      view.baseline?.id ?? null,
      view.values.kind,
      view.values.occurred_on,
      view.values.amount,
      view.values.currency,
    ]);
  async function findSource(reference: SourceReference, signal: AbortSignal) {
    let offset = 0;
    while (!signal.aborted) {
      const result = await api.list(
        sid,
        {
          kind: 'all',
          state: 'all',
          month: '',
          source_result_id: reference.result_id,
          source_event_index: reference.event_index,
          limit: 20,
          offset,
        },
        token,
        signal,
      );
      const found = result.events.find(
        (e) =>
          e.subscription_id === sid &&
          e.sources.some(
            (s) =>
              s.result_id === reference.result_id &&
              s.event_index === reference.event_index,
          ),
      );
      if (found) return found;
      offset += result.limit;
      if (offset >= result.total) return null;
    }
    return null;
  }
  function success(saved: BillingEntry) {
    pending = null;
    previewKey = null;
    publish({
      baseline: saved,
      values: parseBillingValues(saved),
      saved,
      status: 'ready',
      error: null,
      reason: null,
      confirmed: false,
      duplicateConfirmed: false,
      dirty: false,
      uncertain: false,
      conflict: false,
      latest: null,
      duplicates: null,
    });
  }
  const matching = (entry: BillingEntry, body: BillingValues) => {
    const clean = (s: string) => {
      const [w, f = ''] = s.split('.');
      return `${w.replace(/^0+(?=\d)/, '')}.${f.replace(/0+$/, '')}`;
    };
    return (
      entry.kind === body.kind &&
      entry.occurred_on === body.occurred_on &&
      clean(entry.amount) === clean(body.amount) &&
      entry.currency === body.currency &&
      entry.refund_of_id === body.refund_of_id &&
      entry.state === body.state
    );
  };
  async function refreshLatest() {
    if (!allowed() || !view.baseline || view.status === 'saving') return;
    const id = view.baseline.id;
    await read(
      'latest',
      (signal) => api.get(sid, id, token, signal),
      (latest) => {
        if (view.baseline?.id === id) publish({ latest });
      },
    );
  }
  async function resolveSource(reference = source) {
    if (!reference) return;
    await read(
      'source',
      (signal) => findSource(reference, signal),
      (linked) => publish({ linked }),
    );
  }
  async function refreshAttachment(id: string) {
    await read(
      'attachment',
      (signal) => api.get(sid, id, token, signal),
      (latest) => publish({ latest }),
    );
  }
  async function submit(
    receipt: Receipt,
    replay: boolean,
    abort: AbortController,
  ) {
    if (!currentWrite(abort)) return;
    try {
      let saved: BillingEntry;
      if (receipt.type === 'create') {
        if (
          replay &&
          receipt.body.source_result_id !== null &&
          receipt.body.source_event_index !== null
        ) {
          try {
            const linked = await findSource(
              {
                result_id: receipt.body.source_result_id,
                event_index: receipt.body.source_event_index,
              },
              abort.signal,
            );
            if (!currentWrite(abort)) return;
            if (linked) {
              writeAbort = undefined;
              success(linked);
              return;
            }
          } catch (error) {
            if (errorCode(error) !== 'dependency_unavailable') throw error;
          }
        }
        if (!currentWrite(abort)) return;
        saved = await api.create(sid, receipt.body, token, abort.signal);
      } else if (receipt.type === 'update')
        saved = await api.update(
          sid,
          receipt.id,
          receipt.body,
          token,
          abort.signal,
        );
      else
        saved = await api[receipt.type](
          sid,
          receipt.id,
          receipt.body,
          token,
          abort.signal,
        );
      if (currentWrite(abort)) {
        writeAbort = undefined;
        if (
          (receipt.type === 'attach' || receipt.type === 'detach') &&
          receipt.keepDraft
        ) {
          pending = null;
          previewKey = null;
          const changedFacts =
            !view.baseline || !matching(saved, view.baseline);
          publish({
            baseline: changedFacts ? view.baseline : saved,
            saved: null,
            latest: changedFacts ? saved : null,
            status: changedFacts ? 'error' : 'ready',
            uncertain: false,
            conflict: changedFacts,
            confirmed: false,
            duplicateConfirmed: false,
            duplicates: null,
            error: changedFacts ? 'conflict' : null,
            reason: changedFacts ? 'version_conflict' : null,
          });
        } else success(saved);
      }
    } catch (error) {
      if (!currentWrite(abort)) return;
      const code = errorCode(error),
        reason = error instanceof BillingError ? error.reason : null;
      if (code === 'authentication_required' || code === 'access_denied') {
        denied(code);
        return;
      }
      const uncertain = code === 'dependency_unavailable';
      // A lost update response may have committed with the old CAS version.
      if (replay && code === 'conflict' && receipt.type === 'update') {
        try {
          const latest = await api.get(sid, receipt.id, token, abort.signal);
          if (!currentWrite(abort)) return;
          if (matching(latest, receipt.body)) {
            writeAbort = undefined;
            success(latest);
            return;
          }
          publish({ latest });
        } catch (readError) {
          if (!currentWrite(abort)) return;
          const readCode = errorCode(readError);
          if (
            readCode === 'authentication_required' ||
            readCode === 'access_denied'
          ) {
            denied(readCode);
            return;
          }
          publish({ error: readCode });
        }
      }
      writeAbort = undefined;
      if (!uncertain) pending = null;
      const attachment =
        (receipt.type === 'attach' || receipt.type === 'detach') &&
        !receipt.keepDraft;
      publish({
        status: 'error',
        error: code,
        reason,
        uncertain,
        confirmed: false,
        duplicateConfirmed: false,
        conflict:
          code === 'conflict' &&
          !attachment &&
          reason !== 'possible_duplicate' &&
          reason !== 'refund_conflict' &&
          reason !== 'source_conflict' &&
          receipt.type !== 'create',
        ...(code === 'conflict' ? { duplicates: null } : {}),
      });
      if (code === 'conflict') {
        previewKey = null;
        remember();
        if (reason === 'source_conflict' || receipt.type === 'create')
          await resolveSource(
            receipt.type === 'attach' || receipt.type === 'detach'
              ? {
                  result_id: receipt.body.source_result_id,
                  event_index: receipt.body.source_event_index,
                }
              : source,
          );
        else if (attachment) await refreshAttachment(receipt.id);
        else if (view.baseline) await refreshLatest();
      }
    }
  }
  return {
    state: () => view,
    refreshLatest,
    resolveSource,
    refreshAttachment,
    async initializeSource(event: ProcessingEvent) {
      if (frozen() || view.baseline || view.dirty || !source) return;
      await resolveSource();
      if (frozen() || view.baseline || view.dirty || view.error) return;
      if (view.linked) {
        publish({ ...initialBillingView(view.linked), linked: view.linked });
        return;
      }
      publish({
        values: {
          kind: ['invoice', 'payment', 'refund'].includes(event.event_type)
            ? (event.event_type as BillingKind)
            : '',
          occurred_on: event.billed_at ?? '',
          amount: event.amount ?? '',
          currency: event.currency ?? '',
          refund_of_id: null,
          state: 'active',
        },
        dirty: true,
        confirmed: false,
      });
    },
    edit(patch: Partial<BillingDraft>) {
      if (frozen()) return;
      const values = { ...view.values, ...patch };
      if (
        (patch.currency !== undefined &&
          patch.currency !== view.values.currency) ||
        (patch.kind !== undefined && patch.kind !== 'refund')
      )
        values.refund_of_id = null;
      previewKey = null;
      reads.get('duplicates')?.abort();
      reads.delete('duplicates');
      const parentChanged =
        patch.currency !== undefined ||
        patch.kind !== undefined ||
        patch.refund_of_id !== undefined;
      if (parentChanged) {
        reads.get('payment')?.abort();
        reads.delete('payment');
      }
      publish({
        values,
        dirty: true,
        confirmed: false,
        duplicateConfirmed: false,
        duplicates: null,
        saved: null,
        selectedPayment:
          values.refund_of_id === view.selectedPayment?.id
            ? view.selectedPayment
            : null,
        error: null,
        reason: null,
        loading: view.loading.filter(
          (s) => s !== 'duplicates' && (!parentChanged || s !== 'payment'),
        ),
      });
    },
    confirm(confirmed: boolean) {
      if (!frozen() && !view.conflict) publish({ confirmed });
    },
    acknowledgeDuplicate(duplicateConfirmed: boolean) {
      if (!frozen() && view.duplicates && previewKey === duplicateKey())
        publish({ duplicateConfirmed });
    },
    async preview(offset = 0) {
      if (frozen() || view.conflict) return;
      let values: BillingValues;
      try {
        values = parseBillingValues(view.values);
      } catch {
        publish({ error: 'invalid_request' });
        return;
      }
      const captured = duplicateKey();
      await read(
        'duplicates',
        (signal) =>
          api.duplicates(
            sid,
            {
              ...values,
              exclude_id: view.baseline?.id ?? null,
              limit: 20,
              offset,
            },
            token,
            signal,
          ),
        (duplicates) => {
          if (duplicateKey() === captured) {
            previewKey = captured;
            publish({ duplicates, duplicateConfirmed: false });
          }
        },
      );
    },
    async save() {
      if (
        frozen() ||
        view.conflict ||
        !view.confirmed ||
        !view.duplicates ||
        previewKey !== duplicateKey() ||
        (view.duplicates.total > 0 && !view.duplicateConfirmed)
      )
        return;
      let values: BillingValues;
      try {
        values = parseBillingValues(view.values);
      } catch {
        publish({ error: 'invalid_request' });
        return;
      }
      if (view.baseline?.subscription_id !== sid && view.baseline) return;
      const duplicate_confirmed = view.duplicates.total > 0,
        duplicate_token = duplicate_confirmed ? view.duplicates.token : null;
      await runWrite(async (abort) => {
        if (!view.baseline && source) {
          const captured = source;
          const linked = await findSource(captured, abort.signal);
          if (!currentWrite(abort)) return;
          if (linked) {
            writeAbort = undefined;
            publish({ linked, status: 'ready', confirmed: false });
            return;
          }
        }
        if (!currentWrite(abort)) return;
        pending = view.baseline
          ? {
              type: 'update',
              id: view.baseline.id,
              body: Object.freeze({
                ...values,
                expected_version: view.baseline.version,
                confirmed: true,
                duplicate_confirmed,
                duplicate_token,
              }),
            }
          : {
              type: 'create',
              body: Object.freeze({
                ...values,
                request_id: (
                  options.randomUUID ?? (() => crypto.randomUUID())
                )(),
                source_result_id: source?.result_id ?? null,
                source_event_index: source?.event_index ?? null,
                confirmed: true,
                duplicate_confirmed,
                duplicate_token,
              }),
            };
        remember();
        await submit(pending, false, abort);
      });
    },
    async replay() {
      if (!allowed() || !view.uncertain || !pending || view.status === 'saving')
        return;
      const receipt = pending;
      await runWrite((abort) => submit(receipt, true, abort));
    },
    rebase() {
      if (frozen() || !view.latest || view.latest.id !== view.baseline?.id)
        return;
      previewKey = null;
      publish({
        baseline: view.latest,
        latest: null,
        conflict: false,
        confirmed: false,
        duplicateConfirmed: false,
        duplicates: null,
        status: 'ready',
        error: null,
        reason: null,
      });
    },
    discard() {
      if (frozen()) return;
      const baseline =
        view.latest?.id === view.baseline?.id ? view.latest : view.baseline;
      previewKey = null;
      publish({ ...initialBillingView(baseline), linked: view.linked });
    },
    adoptLinked() {
      if (frozen() || !view.linked || view.linked.subscription_id !== sid)
        return;
      pending = null;
      previewKey = null;
      publish({ ...initialBillingView(view.linked), linked: view.linked });
    },
    async loadPayments(offset = 0) {
      if (!allowed()) return;
      await read(
        'payments',
        (signal) =>
          api.list(
            sid,
            { kind: 'payment', state: 'active', month: '', limit: 20, offset },
            token,
            signal,
          ),
        (payments) => publish({ payments }),
      );
    },
    async selectPayment(id: string) {
      if (frozen() || view.values.kind !== 'refund') return;
      const currency = view.values.currency;
      await read(
        'payment',
        (signal) => api.get(sid, id, token, signal),
        (selectedPayment) => {
          if (
            !frozen() &&
            view.values.kind === 'refund' &&
            view.values.currency === currency &&
            selectedPayment.subscription_id === sid &&
            selectedPayment.kind === 'payment' &&
            selectedPayment.state === 'active' &&
            selectedPayment.currency === currency
          ) {
            previewKey = null;
            publish({
              selectedPayment,
              values: { ...view.values, refund_of_id: selectedPayment.id },
              dirty: true,
              confirmed: false,
              duplicateConfirmed: false,
              duplicates: null,
            });
          } else publish({ error: 'invalid_request' });
        },
      );
    },
    async loadSelectedPayment() {
      const id = view.values.refund_of_id;
      if (!id) return;
      await read(
        'payment',
        (signal) => api.get(sid, id, token, signal),
        (selectedPayment) => {
          if (
            view.values.refund_of_id === id &&
            selectedPayment.currency === view.values.currency &&
            view.values.kind === 'refund'
          )
            publish({ selectedPayment });
        },
      );
    },
    async loadRecords(offset = 0) {
      await read(
        'records',
        (signal) =>
          api.list(
            sid,
            { kind: 'all', state: 'all', month: '', limit: 20, offset },
            token,
            signal,
          ),
        (records) => publish({ records }),
      );
    },
    async attachTo(entry: BillingEntry, confirmed: boolean) {
      if (frozen() || !confirmed || !source || entry.subscription_id !== sid)
        return;
      const capturedSource = source;
      await runWrite(async (abort) => {
        pending = {
          type: 'attach',
          id: entry.id,
          body: Object.freeze({
            expected_version: entry.version,
            source_result_id: capturedSource.result_id,
            source_event_index: capturedSource.event_index,
            confirmed: true,
          }),
        };
        remember();
        await submit(pending, false, abort);
      });
    },
    async changeSource(
      type: 'attach' | 'detach',
      reference: SourceReference,
      confirmed: boolean,
    ) {
      if (
        frozen() ||
        !confirmed ||
        !view.baseline ||
        view.baseline.subscription_id !== sid
      )
        return;
      const baseline = view.baseline;
      await runWrite(async (abort) => {
        pending = {
          type,
          id: baseline.id,
          keepDraft: true,
          body: Object.freeze({
            expected_version: baseline.version,
            source_result_id: reference.result_id,
            source_event_index: reference.event_index,
            confirmed: true,
          }),
        };
        remember();
        await submit(pending, false, abort);
      });
    },
    dispose() {
      if (disposed) return;
      if (view.status === 'saving') {
        view = {
          ...view,
          status: pending ? 'error' : 'ready',
          uncertain: pending !== null,
          confirmed: false,
          error: pending ? 'dependency_unavailable' : null,
        };
      }
      remember();
      disposed = true;
      writeAbort?.abort();
      cancelReads();
      source = null;
      pending = null;
      previewKey = null;
      view = { ...initialBillingView(), status: 'disposed' };
      changed(view);
    },
  };
}
