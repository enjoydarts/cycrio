import assert from 'node:assert/strict';
import test from 'node:test';
import { safeAnalysisReturn, automationSetupLink } from './setup-readiness.ts';
const id = '10000000-0000-4000-8000-000000000001';
test('intake return paths preserve only the explicitly supported local wizard scope', () => {
  assert.equal(
    safeAnalysisReturn('/import?account_id=' + id),
    '/import?account_id=' + id,
  );
  assert.equal(
    safeAnalysisReturn('/import?connect=true'),
    '/import?connect=true',
  );
  const link = automationSetupLink(
    '/settings/ai',
    '/import?account_id=' + id,
    id,
  );
  assert.equal(
    new URL(link, 'https://cycrio.invalid').searchParams.get('return_to'),
    '/import?account_id=' + id + '&setup_account=' + id,
  );
  for (const value of [
    '/import?account_id=other',
    '/import?account_id=' + id + '&account_id=' + id,
    '/import?connect=maybe',
    '/import?return_to=https://evil.example',
    '/import?token=secret',
    '//evil.example/import',
    '/import/other',
    '/import?start=true',
  ])
    assert.equal(safeAnalysisReturn(value), '/analysis');
});
