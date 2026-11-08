import assert from 'node:assert/strict';
import test from 'node:test';
import { createTelemetryController } from './telemetry.ts';

const owner = '11111111-1111-4111-8111-111111111111';
const nextOwner = '22222222-2222-4222-8222-222222222222';
const detail = '33333333-3333-4333-8333-333333333333';
const tick = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

function fixture(production = true) {
  const target = new EventTarget();
  const events: any[] = [];
  const hooks: ((event: any) => any)[] = [];
  let now = 1000;
  let sequence = 0;
  let session = '44444444-4444-4444-8444-444444444444';
  let enabled = true;
  let fail = false;
  let captureFails = false;
  let resetCount = 0;
  const controller = createTelemetryController({
    production,
    target,
    origin: 'https://cycrio.com',
    now: () => now,
    newID: () =>
      `55555555-5555-4555-8555-${String(++sequence).padStart(12, '0')}`,
    getConfiguration: async () => {
      if (fail) throw Error('offline');
      return enabled
        ? {
            enabled: true,
            project_token: 'phc_navigation_test',
            host: 'https://us.i.posthog.com',
          }
        : { enabled: false };
    },
    loadSDK: async () => ({
      initialize(_token, options) {
        const before = options.before_send as (event: any) => any;
        hooks.push(before);
        return {
          captureException() {},
          capturePage(event: string, properties: Record<string, unknown>) {
            if (captureFails) throw Error('SDK offline');
            properties.mail_body = 'PRIVATE_MUTATED_SDK_METADATA';
            const filtered = before({
              event,
              uuid: `wire-${events.length}`,
              properties: {
                ...properties,
                $current_url:
                  'https://private.test/user@example.test?token=secret',
                $title: 'private title',
                $referrer: 'private referrer',
                mail_body: 'PRIVATE_MAIL',
                email: 'private@example.test',
                token: 'untrusted-token',
                distinct_id: 'foreign-identity',
              },
            });
            if (filtered) events.push(filtered);
          },
          getSessionId: () => session,
          reset() {
            resetCount++;
            session = '66666666-6666-4666-8666-666666666666';
          },
          disable() {},
        };
      },
    }),
    refreshMilliseconds: 1_000_000,
  });
  return {
    controller,
    target,
    events,
    hooks,
    advance: (milliseconds: number) => {
      now += milliseconds;
    },
    enable: (value: boolean) => {
      enabled = value;
    },
    fail: (value: boolean) => {
      fail = value;
    },
    captureFails: (value = true) => {
      captureFails = value;
    },
    session: (value: string) => {
      session = value;
    },
    resets: () => resetCount,
  };
}

test('manual navigation pairs views and leaves with canonical pages and owned wire fields', async () => {
  const f = fixture();
  try {
    f.controller.setUser(owner);
    f.controller.navigate('/');
    f.controller.start();
    await tick();
    assert.equal(f.events.length, 1);
    f.controller.navigate('/');
    assert.equal(f.events.length, 1);
    f.advance(1500);
    f.controller.navigate('/subscriptions/' + detail);
    assert.deepEqual(
      f.events.map((e) => e.event),
      ['$pageview', '$pageleave', '$pageview'],
    );
    assert.equal(f.events[1].properties.$prev_pageview_duration, 1.5);
    assert.equal(
      f.events[0].properties.$pageview_id,
      f.events[1].properties.$pageview_id,
    );
    assert.equal(f.events[1].properties.$prev_pageview_pathname, '/');
    assert.equal(
      f.events[2].properties.$current_url,
      'https://cycrio.com/subscriptions/detail',
    );
    assert.equal(f.events[2].properties.page_name, 'subscription_detail');
    for (const event of f.events) {
      assert.equal(event.properties.distinct_id, owner);
      assert.equal(
        event.properties.$session_id,
        '44444444-4444-4444-8444-444444444444',
      );
      assert.equal(event.properties.$process_person_profile, false);
      assert.equal(event.properties.$geoip_disable, true);
      assert.equal(event.properties.token, 'phc_navigation_test');
      assert.equal(event.properties.mail_body, undefined);
      assert.doesNotMatch(
        JSON.stringify(event),
        /PRIVATE_MAIL|private|foreign|untrusted|33333333/,
      );
    }
    assert.equal(
      f.hooks[0]({ event: '$pageview', uuid: 'foreign', properties: {} }),
      null,
    );
    assert.equal(
      f.hooks[0]({ event: '$pageleave', uuid: 'foreign', properties: {} }),
      null,
    );
  } finally {
    f.controller.dispose();
  }
});

test('unknown routes close the known view but never send untrusted paths or callback data', async () => {
  const f = fixture();
  try {
    f.controller.setUser(owner);
    f.controller.start();
    await tick();
    for (const path of [
      '/sso-callback',
      '/unknown/private@example.test',
      '/mail?token=SECRET',
      '/mail#SECRET',
    ]) {
      f.controller.navigate(path);
    }
    assert.equal(f.events.length, 0);
    f.controller.navigate('/mail');
    f.controller.navigate('/unknown/private@example.test');
    assert.deepEqual(
      f.events.map((e) => e.event),
      ['$pageview', '$pageleave'],
    );
    assert.doesNotMatch(JSON.stringify(f.events), /SECRET|private|sso/);
  } finally {
    f.controller.dispose();
  }
});

test('initial asynchronous setup captures only the current page without replaying history', async () => {
  const f = fixture();
  try {
    f.controller.setUser(owner);
    f.controller.start();
    f.controller.navigate('/mail');
    f.controller.navigate('/reports');
    await tick();
    assert.equal(f.events.length, 1);
    assert.equal(f.events[0].properties.$pathname, '/reports');
  } finally {
    f.controller.dispose();
  }
});

test('identity changes flush the old owner and session before opening a new view', async () => {
  const f = fixture();
  try {
    f.controller.setUser(owner);
    f.controller.navigate('/mail');
    f.controller.start();
    await tick();
    f.controller.setUser(nextOwner);
    assert.deepEqual(
      f.events.map((e) => e.properties.distinct_id),
      [owner, owner, nextOwner],
    );
    assert.equal(
      f.events[1].properties.$session_id,
      '44444444-4444-4444-8444-444444444444',
    );
    assert.equal(
      f.events[2].properties.$session_id,
      '66666666-6666-4666-8666-666666666666',
    );
    f.controller.setUser(null);
    assert.equal(f.events.at(-1).event, '$pageleave');
    assert.equal(f.events.at(-1).properties.distinct_id, nextOwner);
    assert.equal(f.events.length, 4);
    f.controller.navigate('/');
    assert.match(
      f.events.at(-1).properties.distinct_id,
      /^cycrio-visitor-[0-9a-f-]{36}$/,
    );
    assert.equal(f.resets(), 2);
  } finally {
    f.controller.dispose();
  }
});

test('pagehide and disposal emit one leave and BFCache restoration can open a new view', async () => {
  const f = fixture();
  f.controller.navigate('/');
  f.controller.start();
  await tick();
  f.target.dispatchEvent(new Event('pagehide'));
  f.target.dispatchEvent(new Event('pagehide'));
  assert.equal(f.events.length, 2);
  f.target.dispatchEvent(new Event('pageshow'));
  assert.equal(f.events.length, 2);
  const restored = new Event('pageshow');
  Object.assign(restored, { persisted: true });
  f.target.dispatchEvent(restored);
  assert.equal(f.events.length, 3);
  f.controller.dispose();
  f.controller.dispose();
  assert.equal(f.events.length, 4);
});

test('disabling or losing configuration drops past views and restores only the current page', async () => {
  const f = fixture();
  try {
    f.controller.setUser(owner);
    f.controller.navigate('/mail');
    f.controller.start();
    await tick();
    const old = f.hooks[0];
    f.enable(false);
    await f.controller.refresh();
    f.controller.navigate('/reports');
    assert.equal(f.events.length, 1);
    assert.equal(
      old({ event: '$pageview', properties: {}, uuid: 'stale' }),
      null,
    );
    f.enable(true);
    await f.controller.refresh();
    assert.equal(f.events.length, 2);
    assert.equal(f.events[1].properties.$pathname, '/reports');
    f.fail(true);
    await f.controller.refresh();
    f.controller.leavePage();
    assert.equal(f.events.length, 2);
  } finally {
    f.controller.dispose();
  }
});

test('development, bad session IDs, and failed SDK captures do not break navigation', async () => {
  const development = fixture(false);
  development.controller.navigate('/');
  development.controller.start();
  await tick();
  assert.equal(development.events.length, 0);
  development.controller.dispose();
  const f = fixture();
  try {
    f.controller.setUser(owner);
    f.session('not-a-session');
    f.controller.navigate('/');
    f.controller.start();
    await tick();
    assert.equal(f.events.length, 0);
    f.session('44444444-4444-4444-8444-444444444444');
    await f.controller.refresh();
    assert.equal(f.events.length, 1);
    f.captureFails();
    assert.doesNotThrow(() => f.controller.navigate('/mail'));
    f.captureFails(false);
    await f.controller.refresh();
    assert.equal(f.events.at(-1).event, '$pageview');
    assert.equal(f.events.at(-1).properties.$pathname, '/mail');
    assert.equal(f.events.length, 2);
  } finally {
    f.controller.dispose();
  }
});

test('a new SDK session never replaces the previous view session on leave', async () => {
  const f = fixture();
  try {
    f.controller.setUser(owner);
    f.controller.navigate('/');
    f.controller.start();
    await tick();
    f.session('77777777-7777-4777-8777-777777777777');
    f.advance(100_000_000);
    f.controller.navigate('/mail');
    assert.equal(
      f.events[1].properties.$session_id,
      '44444444-4444-4444-8444-444444444444',
    );
    assert.equal(f.events[1].properties.$prev_pageview_duration, 86400);
    assert.equal(
      f.events[2].properties.$session_id,
      '77777777-7777-4777-8777-777777777777',
    );
  } finally {
    f.controller.dispose();
  }
});
