import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { subscription, id } from './subscriptions-fixtures.ts';
import {
  ScheduleError,
  type ScheduleAPI,
  type ScheduleSave,
  type ScheduleView,
  type SchedulePreview,
} from './schedule-api.ts';
import {
  createScheduleController,
  initialScheduleView,
  type ScheduleDrafts,
} from './schedule-controller.ts';
const rule = {
  interval_unit: 'month' as const,
  interval_count: 1,
  anchor_date: '2026-01-31',
  month_end: true,
};
const view = (): ScheduleView => ({
  subscription: {
    ...subscription(),
    interval_unit: 'month',
    interval_count: 1,
  },
  settings: null,
  as_of: '2026-10-26',
  timezone: 'Asia/Tokyo',
  next_date_source: 'manual',
  overdue: false,
});
const preview = (): SchedulePreview => ({
  subscription: view().subscription,
  rule,
  as_of: view().as_of,
  timezone: 'Asia/Tokyo',
  candidate_date: '2026-10-31',
  reason: '',
  algorithm_version: 'calendar-v1',
  overdue: false,
});
const requestID = '22222222-2222-4222-8222-222222222222';
const result = (body: ScheduleSave) => ({
  receipt: {
    request_id: body.request_id,
    applied_version: body.expected_version + 1,
    rule: {
      interval_unit: body.interval_unit,
      interval_count: body.interval_count,
      anchor_date: body.anchor_date,
      month_end: body.month_end,
    },
    as_of: body.expected_as_of,
    candidate_date: body.expected_candidate,
    algorithm_version: 'calendar-v1' as const,
    created_at: '2026-10-26T00:00:00Z',
  },
  current: {
    ...view(),
    subscription: {
      ...view().subscription,
      version: body.expected_version + 1,
      next_billing_date: body.expected_candidate,
    },
  },
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
  overrides: Partial<ScheduleAPI> = {},
  drafts: ScheduleDrafts = new Map(),
) {
  let denials = 0,
    writes: ScheduleSave[] = [];
  const api: ScheduleAPI = {
    get: async () => view(),
    preview: async () => preview(),
    save: async (_id, body) => {
      writes.push(body);
      return result(body);
    },
    ...overrides,
  };
  const controller = createScheduleController(
    api,
    async () => 'scoped',
    () => {},
    () => denials++,
    { subscription: view().subscription, drafts, requestID: () => requestID },
  );
  return { c: controller, drafts, writes, denials: () => denials };
}
async function prepared(s: ReturnType<typeof setup>) {
  await s.c.load();
  s.c.edit({
    interval_unit: 'month',
    interval_count: '1',
    anchor_date: '2026-01-31',
    month_end: true,
  });
  await s.c.preview();
  s.c.confirm(true);
  return s;
}
test('unknown cycle remains empty; initial GET never previews and uses confirmed snapshot anchor with canonical current cycle', async () => {
  assert.equal(initialScheduleView(subscription()).values.interval_unit, '');
  let posts = 0;
  const s = setup({
    get: async () => ({
      ...view(),
      subscription: {
        ...view().subscription,
        interval_unit: 'week',
        interval_count: 2,
      },
      settings: {
        rule,
        stale: true,
        confirmed_as_of: '2026-10-26',
        confirmed_candidate: '2026-10-31',
        algorithm_version: 'calendar-v1',
        confirmed_version: 1,
        confirmed_at: '2026-10-26T00:00:00Z',
        updated_at: '2026-10-26T00:00:00Z',
      },
    }),
    preview: async () => {
      posts++;
      return preview();
    },
  });
  await s.c.load();
  assert.equal(posts, 0);
  assert.deepEqual(s.c.state().values, {
    interval_unit: 'week',
    interval_count: '2',
    anchor_date: rule.anchor_date,
    month_end: false,
  });
});
test('explicit preview and confirmation are required; edits invalidate candidate and confirmation', async () => {
  const s = setup();
  await s.c.load();
  await s.c.save();
  assert.equal(s.writes.length, 0);
  await prepared(s);
  assert.equal(s.c.state().preview?.candidate_date, '2026-10-31');
  s.c.edit({ interval_count: '2' });
  assert.equal(s.c.state().preview, null);
  assert.equal(s.c.state().confirmed, false);
  await s.c.save();
  assert.equal(s.writes.length, 0);
});
test('frozen unknown write survives close/reopen and failed GET, exact old-day replay returns original receipt plus current edits', async () => {
  const bodies: ScheduleSave[] = [];
  let fail = true;
  const api: Partial<ScheduleAPI> = {
    save: async (_id, body) => {
      bodies.push(body);
      if (fail) throw new ApiError('dependency_unavailable');
      const out = result(body);
      out.current.subscription.version = 4;
      out.current.subscription.next_billing_date = '2027-02-05';
      return out;
    },
  };
  const s = await prepared(setup(api));
  await s.c.save();
  assert.equal(s.c.state().uncertain, true);
  s.c.edit({ anchor_date: '2027-01-31' });
  assert.equal(s.c.state().values.anchor_date, rule.anchor_date);
  s.c.dispose();
  const reopened = setup(
    {
      ...api,
      get: async () => {
        throw new ApiError('dependency_unavailable');
      },
    },
    s.drafts,
  );
  await reopened.c.load();
  assert.equal(reopened.c.state().uncertain, true);
  fail = false;
  await reopened.c.replay();
  assert.deepEqual(bodies[1], bodies[0]);
  assert.equal(bodies[1].expected_as_of, '2026-10-26');
  assert.equal(reopened.c.state().saved?.receipt.applied_version, 2);
  assert.equal(
    reopened.c.state().saved?.current.subscription.next_billing_date,
    '2027-02-05',
  );
});
test('write start invalidates auxiliary reads and duplicate saves independently of displayed status', async () => {
  const read = deferred<ScheduleView>(),
    write = deferred<ReturnType<typeof result>>();
  const s = await prepared(setup());
  // An explicit refresh can overlap an otherwise reviewed save.
  const api = setup(
    {
      get: async () => read.promise,
      save: async (_id, body) => {
        api.writes.push(body);
        return write.promise;
      },
    },
    s.drafts,
  );
  s.c.dispose();
  const reading = api.c.load();
  const saving = api.c.save();
  read.reject(new ApiError('access_denied'));
  await reading;
  assert.equal(api.c.state().status, 'saving');
  assert.equal(api.denials(), 0);
  await api.c.save();
  await api.c.replay();
  assert.equal(api.writes.length, 1);
  write.resolve(result(api.writes[0]));
  await saving;
  assert.ok(api.c.state().saved);
});
test('dispose during write retains uncertainty and old response never publishes to new target', async () => {
  const write = deferred<ReturnType<typeof result>>();
  let body!: ScheduleSave;
  const s = await prepared(
    setup({
      save: async (_id, v) => {
        body = v;
        return write.promise;
      },
    }),
  );
  const pending = s.c.save();
  s.c.dispose();
  const reopened = setup({}, s.drafts);
  assert.equal(reopened.c.state().uncertain, true);
  write.resolve(result(body));
  await pending;
  assert.equal(reopened.c.state().saved, null);
  assert.equal(reopened.c.state().uncertain, true);
});
test('readonly disposal preserves editable draft without loading and late denial is ignored', async () => {
  const read = deferred<ScheduleView>();
  const s = setup({ get: async () => read.promise });
  const pending = s.c.load();
  s.c.dispose();
  read.reject(new ApiError('access_denied'));
  await pending;
  const reopened = setup({}, s.drafts);
  assert.equal(reopened.c.state().loading, null);
  assert.equal(reopened.c.state().status, 'ready');
  assert.equal(s.denials(), 0);
});
test('every current read or write denial purges all drafts and sensitive state', async () => {
  for (const action of ['get', 'preview', 'save'] as const)
    for (const code of ['authentication_required', 'access_denied'] as const) {
      const drafts: ScheduleDrafts = new Map();
      const s = setup({}, drafts);
      await prepared(s);
      const denial = async () => {
        throw new ApiError(code);
      };
      s.c.dispose();
      const next = setup({ [action]: denial }, drafts);
      if (action === 'get') await next.c.load();
      else if (action === 'preview') await next.c.preview();
      else await next.c.save();
      assert.equal(next.c.state().status, 'denied', action);
      assert.equal(next.c.state().baseline, null);
      assert.equal(next.c.state().values.anchor_date, '');
      assert.equal(next.c.state().saved, null);
      assert.equal(drafts.size, 0);
      assert.equal(next.denials(), 1);
    }
});
test('preview with a newer contract never silently rebases dirty conditions; latest retry/rebase requires new preview', async () => {
  const newer = {
    ...view(),
    subscription: { ...view().subscription, version: 2, interval_count: 3 },
  };
  let fail = true;
  const s = setup({
    preview: async () => ({ ...preview(), subscription: newer.subscription }),
    get: async () => {
      if (!s.c.state().baseline) return view();
      if (fail) throw new ApiError('dependency_unavailable');
      return newer;
    },
  });
  await s.c.load();
  s.c.edit({ anchor_date: rule.anchor_date, month_end: true });
  await s.c.preview();
  assert.equal(s.c.state().conflict, true);
  assert.equal(s.c.state().baseline?.subscription.version, 1);
  assert.equal(s.c.state().preview, null);
  await s.c.refreshLatest();
  assert.equal(s.c.state().conflict, true);
  fail = false;
  await s.c.refreshLatest();
  s.c.rebase();
  assert.equal(s.c.state().baseline?.subscription.version, 2);
  assert.equal(s.c.state().values.anchor_date, rule.anchor_date);
  assert.equal(s.c.state().preview, null);
  assert.equal(s.c.state().confirmed, false);
});
test('definite expired preview preserves rule, clears frozen request and requires explicit new preview', async () => {
  const s = await prepared(
    setup({
      save: async () => {
        throw new ScheduleError('conflict', 'preview_expired');
      },
    }),
  );
  await s.c.save();
  assert.equal(s.c.state().uncertain, false);
  assert.equal(s.c.state().preview, null);
  assert.equal(s.c.state().values.anchor_date, rule.anchor_date);
  assert.equal(s.c.state().reason, 'preview_expired');
  await s.c.replay();
  assert.equal(s.c.state().saved, null);
});
test('ended and out-of-range previews never become saveable', async () => {
  for (const reason of ['ended', 'out_of_range'] as const) {
    const s = setup({
      preview: async () => ({ ...preview(), reason, candidate_date: null }),
    });
    await prepared(s);
    await s.c.save();
    assert.equal(s.writes.length, 0);
  }
});
test('same-version refresh keeps dirty inputs while updating JST day and invalidating yesterday preview', async () => {
  let today = '2026-10-26';
  const s = await prepared(
    setup({ get: async () => ({ ...view(), as_of: today }) }),
  );
  const values = { ...s.c.state().values };
  today = '2026-10-27';
  await s.c.load();
  assert.equal(s.c.state().baseline?.as_of, today);
  assert.deepEqual(s.c.state().values, values);
  assert.equal(s.c.state().preview, null);
  assert.equal(s.c.state().confirmed, false);
  assert.equal(s.c.state().conflict, false);
});
test('acknowledged receipt is not restored by disposal during the parent current-contract reload', async () => {
  const s = await prepared(setup());
  await s.c.save();
  assert.equal(s.c.acknowledge()?.subscription.version, 2);
  assert.equal(s.drafts.size, 0);
  s.c.dispose();
  assert.equal(s.drafts.size, 0);
  const next = setup({}, s.drafts);
  assert.equal(next.c.state().saved, null);
  await next.c.load();
  assert.ok(next.c.state().baseline);
});
test('explicit next-day preview updates the displayed Japanese day while preserving original contract version', async () => {
  const s = setup({
    preview: async () => ({ ...preview(), as_of: '2026-10-27' }),
  });
  await s.c.load();
  s.c.edit({ anchor_date: rule.anchor_date, month_end: true });
  await s.c.preview();
  assert.equal(s.c.state().baseline?.as_of, '2026-10-27');
  assert.equal(s.c.state().baseline?.subscription.version, 1);
  assert.equal(s.c.state().preview?.as_of, '2026-10-27');
});
