import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { gettingStartedFixture } from './getting-started-fixtures.ts';
import {
  createGettingStartedController,
  gettingStartedGuide,
} from './getting-started.ts';

test('getting started separates connection, permission, start, import, stop, attention and explored states', () => {
  const page = gettingStartedFixture();
  assert.equal(
    gettingStartedGuide(
      { accounts: [], selected: null, preview: null, automation: null },
      false,
    ).stage,
    'connect',
  );
  const unready = structuredClone(page);
  unready.selected!.folders = [];
  assert.equal(gettingStartedGuide(unready, false).stage, 'connection');
  assert.equal(gettingStartedGuide(page, false).stage, 'start');
  page.automation!.state = 'enabled';
  assert.equal(gettingStartedGuide(page, false).stage, 'importing');
  page.automation!.progress.historical_complete = true;
  assert.equal(gettingStartedGuide(page, false).stage, 'explored');
  assert.match(gettingStartedGuide(page, false).description, /登録/);
  page.automation!.progress.unknown_count = 1;
  assert.equal(gettingStartedGuide(page, false).stage, 'attention');
  assert.match(gettingStartedGuide(page, false).href!, /tab=history/);
  page.automation!.state = 'stopped';
  assert.equal(gettingStartedGuide(page, false).stage, 'stopped');
  page.automation!.state = 'needs_confirmation';
  assert.equal(gettingStartedGuide(page, false).stage, 'confirmation');
});
test('ordinary owners wait for administrator preparation without receiving admin links', () => {
  const page = gettingStartedFixture();
  page.preview!.ready = false;
  page.preview!.blocked_reason = 'policy_disabled';
  const owner = gettingStartedGuide(page, false),
    admin = gettingStartedGuide(page, true);
  assert.equal(owner.stage, 'preparation');
  assert.equal(owner.href, null);
  assert.match(owner.description, /管理者/);
  assert.equal(admin.href, '/settings/ai?stage=permission');
  page.preview!.blocked_reason = 'processing_active';
  assert.equal(gettingStartedGuide(page, false).stage, 'processing');
});
test('changed conditions and unknown automation blockers never claim readiness', () => {
  const page = gettingStartedFixture();
  page.automation!.basis.policy_version++;
  assert.equal(gettingStartedGuide(page, true).stage, 'changed');
  page.automation!.basis.policy_version--;
  page.automation!.ready = false;
  page.automation!.preparation_ready = false;
  page.automation!.preparation_blocked_reasons = ['new_unknown_condition'];
  assert.equal(gettingStartedGuide(page, false).stage, 'confirmation');
});

test('import progress does not promise registration without knowing owner automation preference', () => {
  const page = gettingStartedFixture();
  page.automation!.state = 'enabled';
  assert.doesNotMatch(
    gettingStartedGuide(page, false).description,
    /自動登録されます/,
  );
});
test('processing failures link to that connection history rather than candidate review', () => {
  const page = gettingStartedFixture();
  page.automation!.state = 'enabled';
  page.automation!.progress.failed_count = 10;
  const guide = gettingStartedGuide(page, false);
  assert.ok(guide.href?.includes('tab=history'));
  assert.ok(guide.href?.includes(`account_id=${page.selected!.id}`));
  assert.ok(!guide.href?.includes('tab=attention'));
});

test('owner-fixable connection blockers do not ask the owner to wait for an administrator', () => {
  const page = gettingStartedFixture();
  page.preview!.ready = false;
  page.preview!.blocked_reason = 'folders_required';
  const guide = gettingStartedGuide(page, false);
  assert.equal(guide.href, '/settings/mail');
  assert.doesNotMatch(guide.description, /管理者/);
});

function fixture() {
  const page = gettingStartedFixture(),
    calls: string[] = [];
  let denied = 0;
  const ports = {
    mail: {
      list: async () => {
        calls.push('list');
        return page.accounts;
      },
    },
    processing: {
      preview: async (id: string) => {
        calls.push(`preview:${id}`);
        return { ...page.preview!, account_id: id };
      },
    },
    automation: {
      read: async (id: string) => {
        calls.push(`read:${id}`);
        return { ...page.automation!, account_id: id };
      },
    },
  };
  const controller = createGettingStartedController(
    ports,
    async () => 'token',
    () => {},
    () => denied++,
  );
  return { page, ports, calls, controller, denied: () => denied };
}
test('controller reads only the selected owned account, preserving selection on refresh', async () => {
  const f = fixture();
  const other = {
    ...f.page.selected!,
    id: '99999999-9999-4999-8999-999999999999',
    label: '仕事用メール',
  };
  f.page.accounts.push(other);
  await f.controller.load(other.id);
  assert.equal(f.controller.state().page?.selected?.id, other.id);
  assert.deepEqual(f.calls, [
    'list',
    `preview:${other.id}`,
    `read:${other.id}`,
  ]);
  f.calls.length = 0;
  await f.controller.load(other.id);
  assert.equal(f.controller.state().page?.selected?.id, other.id);
  await f.controller.load('not-an-owned-account');
  assert.equal(f.controller.state().page?.selected?.id, f.page.selected!.id);
});
test('empty accounts do not issue preparation requests and errors are not reported as empty success', async () => {
  const f = fixture();
  f.page.accounts = [];
  await f.controller.load('');
  assert.equal(f.controller.state().status, 'ready');
  assert.deepEqual(f.calls, ['list']);
  f.ports.mail.list = async () => {
    throw new ApiError('dependency_unavailable');
  };
  await f.controller.load('');
  assert.equal(f.controller.state().status, 'error');
});
test('denial purges data and blocks later loads', async () => {
  const f = fixture();
  await f.controller.load('');
  f.ports.automation.read = async () => {
    throw new ApiError('access_denied');
  };
  await f.controller.load('');
  assert.equal(f.controller.state().page, null);
  assert.equal(f.controller.state().status, 'denied');
  assert.equal(f.denied(), 1);
  const count = f.calls.length;
  await f.controller.load('');
  assert.equal(f.calls.length, count);
});
test('late responses cannot overwrite another selection or start more reads after dispose', async () => {
  const f = fixture();
  let resolve!: (value: typeof f.page.accounts) => void;
  f.ports.mail.list = () =>
    new Promise((done) => {
      resolve = done;
    });
  const pending = f.controller.load('');
  f.controller.dispose();
  resolve(f.page.accounts);
  await pending;
  assert.equal(f.controller.state().status, 'disposed');
  assert.equal(f.controller.state().page, null);
  assert.deepEqual(f.calls, []);
  const next = fixture();
  let old!: (value: NonNullable<typeof next.page.automation>) => void;
  next.ports.automation.read = async () =>
    (await new Promise((done) => {
      old = done;
    }))!;
  const first = next.controller.load('');
  await Promise.resolve();
  await Promise.resolve();
  next.ports.automation.read = async () => ({
    ...next.page.automation!,
    state: 'stopped',
  });
  await next.controller.load('');
  old(next.page.automation!);
  await first;
  assert.equal(next.controller.state().page?.automation?.state, 'stopped');
});
