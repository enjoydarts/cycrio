import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';
import { gettingStartedFixture } from './getting-started-fixtures.ts';
import {
  automationLastStart,
  automationNextCheck,
} from './mail-automation-activity.ts';
import { automationProgressStatus } from './mail-automation-progress.ts';
import { parseOverview } from './overview-api.ts';
import { overviewFixture } from './overview-fixtures.ts';
const Activity = (await import('./MailAutomationActivity.svelte')).default;
const fixture = () => {
  const s = gettingStartedFixture().automation!;
  return {
    ...s,
    state: 'enabled' as const,
    ready: true,
    running: false,
    last_started_at: '2026-11-03T08:50:00Z',
    last_success_at: '2026-11-03T08:50:10Z',
    next_check_at: '2026-11-03T08:55:10Z',
    retry_at: null,
    progress: {
      ...s.progress,
      bootstrap_complete: true,
      historical_complete: true,
      pending_count: 0,
    },
  };
};
test('activity distinguishes execution start from successful check and never uses a queued request as execution', () => {
  const s = fixture();
  assert.equal(
    automationLastStart({ ...s, recent_sync_runs: [] }),
    s.last_started_at,
  );
  assert.equal(
    automationLastStart({ ...s, last_started_at: null, recent_sync_runs: [] }),
    null,
  );
  assert.equal(
    automationNextCheck(s, Date.parse('2026-11-03T08:52:00Z')).at,
    s.next_check_at,
  );
  assert.equal(automationProgressStatus(s), '次の確認待ち');
  assert.equal(
    automationProgressStatus({ ...s, running: true }),
    'メール確認中',
  );
});
test('workspace overview retains automation metadata needed by the connection indicator', () => {
  const base = overviewFixture();
  const result = parseOverview({
    ...base,
    connections: base.connections.map((row) => ({
      ...row,
      automation_reason: 'tls_failed',
      automation_running: false,
      last_started_at: '2026-11-03T08:50:00Z',
      retry_at: null,
    })),
  });
  assert.equal(result.connections[0].automation_reason, 'tls_failed');
  assert.equal(result.connections[0].automation_running, false);
  assert.equal(result.connections[0].last_started_at, '2026-11-03T08:50:00Z');
});
test('stopped and blocked imports never advertise an old next check, and retry is distinct', () => {
  const s = fixture(),
    now = Date.parse('2026-11-03T08:52:00Z');
  assert.deepEqual(automationNextCheck({ ...s, state: 'stopped' }, now), {
    at: null,
    label: '停止中（予定なし）',
    overdue: false,
  });
  assert.equal(automationNextCheck({ ...s, ready: false }, now).at, null);
  assert.equal(
    automationNextCheck({ ...s, retry_at: '2026-11-03T08:57:00Z' }, now).label,
    '再試行',
  );
  assert.equal(
    automationNextCheck(s, Date.parse('2026-11-03T09:00:00Z')).overdue,
    true,
  );
});
test('shared activity renders all three timestamps in Japan time without a stale next date while stopped', () => {
  const s = fixture();
  const html = render(Activity, { props: { state: s } }).body;
  for (const label of ['最終実行開始', '最終確認成功', '次の確認予定'])
    assert.ok(html.includes(label));
  assert.match(html, /17:50/);
  const stopped = render(Activity, {
    props: { state: { ...s, state: 'stopped' } },
  }).body;
  assert.match(stopped, /停止中（予定なし）/);
  assert.doesNotMatch(stopped, /17:55/);
});
