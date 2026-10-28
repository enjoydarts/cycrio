import { account, ids } from './mail-test-fixtures.ts';
import { processingPreview } from './mail-processing-fixtures.ts';
import type { GettingStartedPage } from './getting-started.ts';

export function gettingStartedFixture(): GettingStartedPage {
  const preview = processingPreview({
    ready: true,
    blocked_reason: '',
    pending_count: 0,
    failed_count: 0,
    unknown_count: 0,
  });
  const selected = account({
    id: preview.account_id,
    state: 'ready',
    folders: [
      {
        id: ids.folder,
        name: 'INBOX',
        delimiter: '/',
        selectable: true,
        available: true,
        selected: true,
      },
    ],
  });
  return {
    accounts: [selected],
    selected,
    preview,
    automation: {
      id: '',
      account_id: selected.id,
      version: 0,
      generation: 0,
      state: 'not_configured',
      reason: '',
      ready: true,
      preparation_ready: true,
      preparation_blocked_reasons: [],
      blocked_reasons: [],
      scope: null,
      default_scope: {
        since: '2025-10-28',
        before: '2026-10-29',
        timezone: 'Asia/Tokyo',
        full_email: true,
        interval_seconds: 300,
        auto_register: true,
      },
      basis: {
        account_version: 1,
        runtime_version: preview.runtime_version,
        profile_id: preview.profile_id!,
        policy_version: preview.policy_version,
      },
      progress: {
        bootstrap_complete: false,
        historical_complete: false,
        discovered_count: 0,
        pending_count: 0,
        completed_count: 0,
        failed_count: 0,
        unknown_count: 0,
      },
      last_success_at: null,
      next_check_at: null,
      discovery_failure_count: 0,
      retry_at: null,
      recent_sync_runs: [],
    },
  };
}
