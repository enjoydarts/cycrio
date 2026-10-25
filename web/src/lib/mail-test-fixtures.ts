import type { MailAccount, MailProbe } from './mail-api.ts';
export const ids = {
  account: '10000000-0000-4000-8000-000000000001',
  other: '10000000-0000-4000-8000-000000000002',
  probe: '20000000-0000-4000-8000-000000000001',
  request: '30000000-0000-4000-8000-000000000001',
  folder: '40000000-0000-4000-8000-000000000001',
};
export function probe(patch: Partial<MailProbe> = {}): MailProbe {
  return {
    id: ids.probe,
    account_id: ids.account,
    state: 'queued',
    error_code: '',
    created_at: '2026-10-25T00:00:00Z',
    started_at: null,
    finished_at: null,
    ...patch,
  };
}
export function account(patch: Partial<MailAccount> = {}): MailAccount {
  return {
    id: ids.account,
    provider: 'fastmail',
    label: '個人のメール',
    state: 'unverified',
    version: 1,
    credential_version: 1,
    checked_at: null,
    last_error: '',
    created_at: '2026-10-25T00:00:00Z',
    updated_at: '2026-10-25T00:00:00Z',
    latest_probe: null,
    folders: [],
    ...patch,
  };
}
