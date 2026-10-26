import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import {
  ScheduleError,
  type ScheduleAPI,
  type ScheduleSave,
  type ScheduleView,
} from './schedule-api.ts';
import {
  createScheduleController,
  type ScheduleDrafts,
} from './schedule-controller.ts';
import {
  historyPreview,
  historyRule,
  historyBasis,
} from './schedule-history-fixtures.ts';
import type {
  ScheduleHistoryAPI,
  ScheduleHistoryPreview,
} from './schedule-history-api.ts';
import {
  createScheduleHistoryController,
  initialHistoryView,
} from './schedule-history-controller.ts';
const baseline = (): ScheduleView => ({
  subscription: historyPreview().subscription,
  settings: null,
  as_of: '2026-10-26',
  timezone: 'Asia/Tokyo',
  next_date_source: 'unknown',
  overdue: false,
});
const deferred = <T>() => {
  let resolve!: (v: T) => void, reject!: (e: unknown) => void;
  const promise = new Promise<T>((a, b) => {
    resolve = a;
    reject = b;
  });
  return { promise, resolve, reject };
};
function setup(
  options: {
    history?: ScheduleHistoryAPI['preview'];
    save?: ScheduleAPI['save'];
    get?: ScheduleAPI['get'];
    preview?: ScheduleAPI['preview'];
    drafts?: ScheduleDrafts;
  } = {},
) {
  const writes: ScheduleSave[] = [];
  let reads = 0,
    denied = 0;
  const api: ScheduleAPI = {
    get: options.get ?? (async () => baseline()),
    preview:
      options.preview ??
      (async (_id, rule) => ({
        ...baseline(),
        rule,
        candidate_date: '2026-10-31',
        reason: '',
        algorithm_version: 'calendar-v1',
      })),
    save:
      options.save ??
      (async (_id, b) => {
        writes.push(b);
        return {
          receipt: {
            request_id: b.request_id,
            applied_version: 2,
            rule: historyRule,
            as_of: b.expected_as_of,
            candidate_date: b.expected_candidate,
            algorithm_version: 'calendar-v1',
            created_at: '2026-10-26T00:00:00Z',
          },
          current: {
            ...baseline(),
            subscription: { ...baseline().subscription, version: 2 },
          },
        };
      }),
  };
  const drafts = options.drafts ?? new Map();
  const c = createScheduleController(
    api,
    async () => 'token',
    () => {},
    () => denied++,
    {
      subscription: baseline().subscription,
      drafts,
      requestID: () => '22222222-2222-4222-8222-222222222222',
      historyAPI: {
        preview: async (...args) => {
          reads++;
          return options.history ? options.history(...args) : historyPreview();
        },
      },
    },
  );
  return { c, drafts, writes, reads: () => reads, denied: () => denied };
}
async function candidate(s: ReturnType<typeof setup>) {
  await s.c.load();
  s.c.toggleHistory(true);
  s.c.editHistory({ start_date: '2026-01-01', currency: 'JPY' });
  await s.c.fetchHistory();
  s.c.adoptHistory(0);
  return s;
}
test('history is explicit, starts with blank boundary and no selection; filters never replace dirty values', async () => {
  const s = setup();
  await s.c.load();
  s.c.toggleHistory(true);
  assert.equal(s.reads(), 0);
  assert.equal(s.c.state().history.filter.start_date, '');
  s.c.edit({
    anchor_date: '2026-01-01',
    interval_unit: 'week',
    interval_count: '2',
  });
  const before = { ...s.c.state().values };
  s.c.editHistory({ start_date: '2026-01-01', currency: 'JPY' });
  await s.c.fetchHistory();
  assert.equal(s.reads(), 1);
  assert.deepEqual(s.c.state().values, before);
  assert.equal(s.c.state().historyBasis, null);
  s.c.adoptHistory(0);
  assert.equal(s.c.state().values.anchor_date, historyRule.anchor_date);
  assert.deepEqual(s.c.state().historyBasis, historyBasis());
  assert.equal(s.c.state().preview, null);
  assert.equal(s.c.state().confirmed, false);
  assert.equal(s.writes.length, 0);
  s.c.editHistory({ currency: 'USD' });
  assert.deepEqual(s.c.state().historyBasis, historyBasis());
});
test('no-op manual patch preserves basis; actual rule change explicitly switches to manual and manual save omits basis', async () => {
  const s = await candidate(setup());
  s.c.edit({ interval_count: '1' });
  assert.deepEqual(s.c.state().historyBasis, historyBasis());
  s.c.edit({ interval_count: '01' });
  assert.deepEqual(s.c.state().historyBasis, historyBasis());
  s.c.edit({ interval_count: '2' });
  assert.equal(s.c.state().historyBasis, null);
  assert.equal(s.c.state().historyManual, true);
  await s.c.preview();
  s.c.confirm(true);
  await s.c.save();
  assert.equal('history_basis' in s.writes[0], false);
});
test('ambiguous candidates stay unselected and ended or out of range cannot be adopted', async () => {
  const ambiguous = {
    ...historyPreview(),
    status: 'ambiguous' as const,
    candidates: [
      ...historyPreview().candidates,
      {
        rule: { ...historyRule, month_end: false },
        candidate_date: '2026-10-31',
        reason: '' as const,
      },
    ],
  };
  const s = setup({ history: async () => ambiguous });
  await s.c.load();
  s.c.editHistory({ start_date: '2026-01-01', currency: 'JPY' });
  await s.c.fetchHistory();
  assert.equal(s.c.state().historyBasis, null);
  s.c.adoptHistory(1);
  assert.equal(s.c.state().values.month_end, false);
  for (const reason of ['ended', 'out_of_range'] as const) {
    const s = setup({
      history: async () => ({
        ...historyPreview(),
        candidates: [{ rule: historyRule, candidate_date: null, reason }],
      }),
    });
    await candidate(s);
    assert.equal(s.c.state().historyBasis, null);
  }
});
test('readonly newer contract requires explicit rebase and preserves original values and baseline', async () => {
  const newer = {
    ...baseline(),
    subscription: { ...baseline().subscription, version: 2 },
  };
  let calls = 0;
  const s = setup({
    get: async () => (++calls === 1 ? baseline() : newer),
    history: async () => ({
      ...historyPreview(),
      subscription: newer.subscription,
    }),
  });
  await s.c.load();
  s.c.edit({ anchor_date: '2026-02-01' });
  s.c.editHistory({ start_date: '2026-01-01', currency: 'JPY' });
  await s.c.fetchHistory();
  assert.equal(s.c.state().baseline?.subscription.version, 1);
  assert.equal(s.c.state().values.anchor_date, '2026-02-01');
  assert.equal(s.c.state().conflict, true);
  s.c.adoptHistory(0);
  assert.equal(s.c.state().historyBasis, null);
  await s.c.refreshLatest();
  s.c.rebase();
  assert.equal(s.c.state().baseline?.subscription.version, 2);
  assert.equal(s.c.state().preview, null);
});
test('new Japanese day invalidates confirmation and requires history selection again when adopted basis is old', async () => {
  const s = await candidate(
    setup({
      preview: async (_id, rule) => ({
        ...baseline(),
        as_of: '2026-10-27',
        rule,
        candidate_date: '2026-10-31',
        reason: '',
        algorithm_version: 'calendar-v1',
      }),
    }),
  );
  await s.c.preview();
  s.c.confirm(true);
  await s.c.save();
  assert.equal(s.writes.length, 0);
  assert.equal(s.c.state().historyNeedsReview, true);
  assert.equal(s.c.state().historyBasis, null);
});
test('history_changed keeps draft and baseline but only fresh retrieve and selection unlocks history save', async () => {
  const s = await candidate(
    setup({
      save: async () => {
        throw new ScheduleError('conflict', 'history_changed');
      },
    }),
  );
  await s.c.preview();
  s.c.confirm(true);
  await s.c.save();
  assert.equal(s.c.state().baseline?.subscription.version, 1);
  assert.equal(s.c.state().values.anchor_date, historyRule.anchor_date);
  assert.equal(s.c.state().conflict, false);
  assert.equal(s.c.state().historyNeedsReview, true);
  assert.equal(s.c.state().history.result, null);
  await s.c.preview();
  s.c.confirm(true);
  assert.equal(s.c.state().confirmed, false);
  s.c.adoptHistory(0);
  assert.equal(s.c.state().historyBasis, null);
  await s.c.fetchHistory();
  assert.equal(s.c.state().historyNeedsReview, true);
  s.c.adoptHistory(0);
  assert.equal(s.c.state().historyNeedsReview, false);
});
test('history auth denial purges every draft; stale, superseded and disposed responses cannot publish or deny', async () => {
  for (const code of ['authentication_required', 'access_denied'] as const) {
    const s = setup({
      history: async () => {
        throw new ApiError(code);
      },
    });
    await s.c.load();
    s.c.editHistory({ start_date: '2026-01-01', currency: 'JPY' });
    await s.c.fetchHistory();
    assert.equal(s.denied(), 1);
    assert.equal(s.drafts.size, 0);
    assert.equal(s.c.state().history.result, null);
    assert.equal(s.c.state().values.anchor_date, '');
  }
  const waiting = deferred<ScheduleHistoryPreview>();
  let signal: AbortSignal | undefined;
  const s = setup({
    history: async (_id, _filter, _token, abort) => {
      signal = abort;
      return waiting.promise;
    },
  });
  await s.c.load();
  s.c.editHistory({ start_date: '2026-01-01', currency: 'JPY' });
  const read = s.c.fetchHistory();
  s.c.editHistory({ currency: 'USD' });
  assert.equal(signal?.aborted, true);
  waiting.reject(new ApiError('access_denied'));
  await read;
  assert.equal(s.denied(), 0);
  assert.equal(s.c.state().history.result, null);
});
test('history draft survives tabs/reopen, frozen request keeps basis and save cancels every auxiliary read', async () => {
  const bodies: ScheduleSave[] = [];
  const waiting = deferred<ScheduleHistoryPreview>();
  let signal: AbortSignal | undefined,
    fail = true;
  const save: ScheduleAPI['save'] = async (_id, b) => {
    bodies.push(b);
    if (fail) throw new ApiError('dependency_unavailable');
    return {
      receipt: {
        request_id: b.request_id,
        applied_version: 2,
        rule: historyRule,
        as_of: b.expected_as_of,
        candidate_date: b.expected_candidate,
        algorithm_version: 'calendar-v1',
        created_at: '2026-10-26T00:00:00Z',
      },
      current: {
        ...baseline(),
        subscription: {
          ...baseline().subscription,
          version: 4,
          next_billing_date: '2027-02-05',
        },
      },
    };
  };
  const first = await candidate(setup({ save }));
  first.c.dispose();
  const s = setup({
    drafts: first.drafts,
    save,
    history: async (_id, _filter, _token, abort) => {
      signal = abort;
      return waiting.promise;
    },
  });
  assert.equal(s.c.state().history.expanded, true);
  assert.deepEqual(s.c.state().historyBasis, historyBasis());
  await s.c.preview();
  s.c.confirm(true);
  const read = s.c.fetchHistory();
  await s.c.save();
  assert.equal(signal?.aborted, true);
  waiting.reject(new ApiError('access_denied'));
  await read;
  assert.equal(s.denied(), 0);
  assert.equal(s.c.state().uncertain, true);
  assert.deepEqual(bodies[0].history_basis, historyBasis());
  assert.equal(Object.isFrozen(bodies[0].history_basis?.filter), true);
  s.c.dispose();
  const reopened = setup({ drafts: s.drafts, save });
  fail = false;
  await reopened.c.replay();
  assert.deepEqual(bodies[1], bodies[0]);
  assert.equal(
    reopened.c.acknowledge()?.subscription.next_billing_date,
    '2027-02-05',
  );
});
test('history read disposal and superseding requests ignore old denial and old scope', async () => {
  const old = deferred<ScheduleHistoryPreview>();
  let count = 0;
  const s = setup({
    history: async (_id, filter) =>
      ++count === 1 ? old.promise : { ...historyPreview(), filter },
  });
  await s.c.load();
  s.c.editHistory({ start_date: '2026-01-01', currency: 'JPY' });
  const pending = s.c.fetchHistory();
  s.c.editHistory({ currency: 'USD' });
  await s.c.fetchHistory();
  old.resolve(historyPreview());
  await pending;
  assert.equal(s.c.state().history.result?.filter.currency, 'USD');
  const late = deferred<ScheduleHistoryPreview>();
  const a = setup({ history: async () => late.promise });
  await a.c.load();
  a.c.editHistory({ start_date: '2026-01-01', currency: 'JPY' });
  const disposed = a.c.fetchHistory();
  a.c.dispose();
  const reopened = setup({ drafts: a.drafts });
  late.reject(new ApiError('access_denied'));
  await disposed;
  assert.equal(a.denied(), 0);
  assert.equal(reopened.c.state().history.loading, false);
  assert.equal(reopened.c.state().history.result, null);
});
test('auxiliary controller purges its own private filter and results on denial, independently of parent state', async () => {
  let shown = {
    ...initialHistoryView('JPY'),
    expanded: true,
    filter: { start_date: '2026-01-01', currency: 'JPY' },
    result: historyPreview(),
  };
  let denied = 0;
  const c = createScheduleHistoryController({
    id: baseline().subscription.id,
    api: {
      preview: async () => {
        throw new ApiError('access_denied');
      },
    },
    token: async () => 'token',
    initial: shown,
    frozen: () => false,
    changed: (next) => {
      shown = next as typeof shown;
    },
    observed: () => {},
    denied: () => denied++,
  });
  await c.fetch();
  c.toggle(true);
  assert.equal(shown.filter.currency, '');
  assert.equal(shown.filter.start_date, '');
  assert.equal(shown.result, null);
  assert.equal(denied, 1);
});
test('history observing a new day cancels an older in-flight calculation before it can restore confirmation', async () => {
  const old = deferred<Awaited<ReturnType<ScheduleAPI['preview']>>>();
  let signal: AbortSignal | undefined;
  const s = setup({
    preview: async (_id, _rule, _token, abort) => {
      signal = abort;
      return old.promise;
    },
    history: async () => ({ ...historyPreview(), as_of: '2026-10-27' }),
  });
  await s.c.load();
  s.c.edit({ ...historyRule, interval_count: '1' });
  const pending = s.c.preview();
  s.c.editHistory({ start_date: '2026-01-01', currency: 'JPY' });
  await s.c.fetchHistory();
  assert.equal(signal?.aborted, true);
  old.resolve({
    subscription: baseline().subscription,
    rule: historyRule,
    as_of: '2026-10-26',
    timezone: 'Asia/Tokyo',
    candidate_date: '2026-10-31',
    reason: '',
    algorithm_version: 'calendar-v1',
    overdue: false,
  });
  await pending;
  s.c.confirm(true);
  assert.equal(s.c.state().preview, null);
  assert.equal(s.c.state().confirmed, false);
});
