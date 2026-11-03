import assert from 'node:assert/strict';
import test from 'node:test';
import { mailConnectionIndicator } from './mail-connection-indicator.ts';
import type { OverviewConnection } from './overview-api.ts';

const account = {
  id: 'mail',
  state: 'ready' as const,
  last_error: '' as const,
};
const enabled: OverviewConnection = {
  id: 'mail',
  label: 'メール',
  state: 'ready',
  automation_state: 'enabled',
  ready: true,
  blocked_reasons: [],
  last_success_at: '2026-11-03T01:00:00Z',
  next_check_at: '2026-11-03T01:05:00Z',
};

test('connected account turns gray when automatic imports stop and cannot turn green without overview', () => {
  assert.equal(mailConnectionIndicator(account, enabled).tone, 'ready');
  const stopped = mailConnectionIndicator(account, {
    ...enabled,
    automation_state: 'stopped',
  });
  assert.equal(stopped.tone, 'neutral');
  assert.match(stopped.label, /停止中/);
  assert.equal(mailConnectionIndicator(account, null).tone, 'neutral');
  assert.match(mailConnectionIndicator(account, null).label, /未確認/);
});

test('running, confirmation, AI blocking and retry states expose their actual state', () => {
  assert.equal(
    mailConnectionIndicator(account, { ...enabled, automation_running: true })
      .tone,
    'running',
  );
  assert.match(
    mailConnectionIndicator(account, {
      ...enabled,
      automation_state: 'needs_confirmation',
    }).label,
    /再確認/,
  );
  const blocked = mailConnectionIndicator(account, {
    ...enabled,
    ready: false,
    blocked_reasons: ['runtime_inactive'],
  });
  assert.equal(blocked.tone, 'warning');
  assert.match(blocked.label, /AI/);
  const retry = mailConnectionIndicator(account, {
    ...enabled,
    retry_at: '2026-11-03T01:10:00Z',
  });
  assert.equal(retry.tone, 'warning');
  assert.match(retry.label, /再試行/);
  assert.equal(
    mailConnectionIndicator(account, {
      ...enabled,
      automation_state: 'not_configured',
    }).tone,
    'neutral',
  );
});

test('mail authentication and connection errors outrank enabled automation', () => {
  const reauth = mailConnectionIndicator(
    { ...account, state: 'needs_reauth' },
    enabled,
  );
  assert.equal(reauth.tone, 'error');
  assert.match(reauth.label, /再認証/);
  assert.equal(
    mailConnectionIndicator(
      { ...account, last_error: 'connection_failed' },
      enabled,
    ).tone,
    'error',
  );
  assert.equal(
    mailConnectionIndicator({ ...account, state: 'disconnected' }, enabled)
      .tone,
    'neutral',
  );
});

test('automatic TLS and exhausted connection stops are red while owner stop is gray', () => {
  for (const reason of [
    'tls_failed',
    'authentication_failed',
    'credentials_unavailable',
    'connection_failed',
    'timeout',
  ]) {
    const result = mailConnectionIndicator(account, {
      ...enabled,
      automation_state: 'stopped',
      automation_reason: reason,
    });
    assert.equal(result.tone, 'error', reason);
    assert.match(result.label, /停止中/);
    assert.match(result.label, /接続|認証|時間/);
  }
  assert.equal(
    mailConnectionIndicator(account, {
      ...enabled,
      automation_state: 'stopped',
      automation_reason: 'owner_stopped',
    }).tone,
    'neutral',
  );
});

test('accessible label includes valid last success and next check, excludes invalid timestamps', () => {
  const result = mailConnectionIndicator(account, enabled);
  assert.match(result.label, /最終成功.*2026\/11\/03.*10:00/);
  assert.match(result.label, /次回確認.*2026\/11\/03.*10:05/);
  const invalid = mailConnectionIndicator(account, {
    ...enabled,
    last_success_at: 'invalid',
    next_check_at: 'invalid',
  });
  assert.doesNotMatch(invalid.label, /Invalid|最終成功|次回確認/);
});

test('stopped or blocked imports do not advertise an inactive next check or retry schedule', () => {
  for (const state of [
    { automation_state: 'stopped' as const, ready: true },
    { automation_state: 'enabled' as const, ready: false },
  ]) {
    const result = mailConnectionIndicator(account, {
      ...enabled,
      ...state,
      last_started_at: '2026-11-03T01:01:00Z',
    });
    assert.doesNotMatch(result.label, /次回確認|10:05/);
    assert.match(result.label, /最終成功.*10:00/);
    assert.match(result.label, /最終実行開始.*10:01/);
  }
  const stoppedRetry = mailConnectionIndicator(account, {
    ...enabled,
    automation_state: 'stopped',
    retry_at: '2026-11-03T01:10:00Z',
  });
  assert.doesNotMatch(stoppedRetry.label, /次回確認|再試行予定|10:05|10:10/);
  const enabledRetry = mailConnectionIndicator(account, {
    ...enabled,
    retry_at: '2026-11-03T01:10:00Z',
  });
  assert.doesNotMatch(enabledRetry.label, /次回確認|10:05/);
  assert.match(enabledRetry.label, /再試行予定.*10:10/);
});
