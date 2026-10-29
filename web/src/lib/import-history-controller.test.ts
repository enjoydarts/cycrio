import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import type { MailAutomationState } from './mail-automation-api.ts';
import { createImportHistory } from './import-history-controller.ts';
const snapshot = (account: string, configured = true): MailAutomationState => ({
  account_id: account,
  id: configured ? 'automation' : '',
  state: configured ? 'enabled' : 'not_configured',
  version: 1,
  generation: 1,
  reason: '',
  ready: true,
  preparation_ready: true,
  preparation_blocked_reasons: [],
  blocked_reasons: [],
  scope: null,
  default_scope: {
    since: '2025-01-01',
    before: '2026-01-01',
    timezone: 'Asia/Tokyo',
    full_email: true,
    interval_seconds: 300,
    auto_register: true,
  },
  basis: {
    account_version: 1,
    runtime_version: 1,
    profile_id: 'profile',
    policy_version: 1,
  },
  last_success_at: null,
  next_check_at: null,
  retry_at: null,
  discovery_failure_count: 0,
  recent_sync_runs: [],
  progress: {
    bootstrap_complete: false,
    historical_complete: false,
    discovered_count: 0,
    pending_count: 0,
    completed_count: 0,
    failed_count: 0,
    unknown_count: 0,
  },
});
test('history shows a started intake with no jobs and reads only the supplied account scope', async () => {
  const calls: string[] = [];
  const controller = createImportHistory(
    {
      read: async (id) => {
        calls.push(id);
        return snapshot(id, id === 'selected');
      },
    },
    async () => 'token',
    () => {},
    () => {},
  );
  await controller.load([
    { id: 'selected', label: 'Personal' },
    { id: 'unused', label: 'Unused' },
  ]);
  assert.deepEqual(calls, ['selected', 'unused']);
  assert.equal(controller.state().page?.length, 1);
  assert.equal(controller.state().page?.[0].state.progress.discovered_count, 0);
  assert.equal(controller.state().page?.[0].label, 'Personal');
  calls.length = 0;
  await controller.load([{ id: 'selected', label: 'Personal' }]);
  assert.deepEqual(calls, ['selected']);
});
test('a later poll discovers newly started imports and permission loss clears old progress', async () => {
  let configured = false,
    denied = false,
    denials = 0;
  const controller = createImportHistory(
    {
      read: async (id) => {
        if (denied) throw new ApiError('access_denied');
        return snapshot(id, configured);
      },
    },
    async () => 'token',
    () => {},
    () => denials++,
  );
  const accounts = [{ id: 'selected', label: 'Personal' }];
  await controller.load(accounts);
  assert.deepEqual(controller.state().page, []);
  configured = true;
  await controller.load(accounts);
  assert.equal(controller.state().page?.length, 1);
  denied = true;
  await controller.load(accounts);
  assert.equal(controller.state().page, null);
  assert.equal(controller.state().status, 'denied');
  assert.equal(denials, 1);
});
