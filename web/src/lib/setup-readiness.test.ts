import assert from 'node:assert/strict';
import test from 'node:test';
import {
  setupReadiness,
  safeAnalysisReturn,
  processingAction,
} from './setup-readiness.ts';
import { processingPreview } from './mail-processing-fixtures.ts';
test('permission is based on current processing preview, not fresh metadata or unknown price', () => {
  assert.equal(
    setupReadiness({
      loaded: true,
      accounts: 1,
      preview: processingPreview(),
      admin: true,
    }).stage,
    'ready',
  );
  assert.equal(
    setupReadiness({ loaded: false, accounts: 0, preview: null, admin: true })
      .stage,
    'loading',
  );
  assert.equal(
    setupReadiness({
      loaded: true,
      accounts: 1,
      preview: null,
      admin: true,
      error: true,
    }).stage,
    'error',
  );
  assert.equal(
    setupReadiness({
      loaded: true,
      accounts: 1,
      preview: processingPreview({
        ready: false,
        blocked_reason: 'policy_disabled',
      }),
      admin: true,
    }).stage,
    'permission',
  );
});
test('return paths allow analysis/mail context only and preserve safe query', () => {
  assert.equal(
    safeAnalysisReturn('/analysis?state=failed&account=abc'),
    '/analysis?state=failed&account=abc',
  );
  assert.equal(
    safeAnalysisReturn(
      '/analysis?account_id=abc&state=failed&offset=20&tab=all',
    ),
    '/analysis?account_id=abc&state=failed&offset=20&tab=all',
  );
  for (const value of [
    '//evil.test',
    'https://evil.test',
    '/settings/ai',
    '/analysis/../settings',
    '/analysis#bad',
    '/analysis?return_to=https://evil.test',
  ])
    assert.equal(safeAnalysisReturn(value), '/analysis');
  assert.equal(
    processingAction('policy_reconfirmation_required', false)?.href,
    null,
  );
  assert.equal(
    processingAction('policy_disabled', true)?.href,
    '/settings/ai?stage=permission',
  );
});
