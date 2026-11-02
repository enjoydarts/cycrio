import assert from 'node:assert/strict';
import test from 'node:test';
import { gettingStartedFixture } from './getting-started-fixtures.ts';
import {
  automationProgressStatus,
  automationProgressDescription,
} from './mail-automation-progress.ts';

test('administrator AI blockers wait automatically without owner reconfirmation', () => {
  for (const reason of [
    'runtime_inactive',
    'policy_disabled',
    'full_email_not_allowed',
    'policy_reconfirmation_required',
    'configuration_unavailable',
    'configuration_changed',
  ]) {
    const s = gettingStartedFixture().automation!;
    s.state = 'enabled';
    s.ready = false;
    s.preparation_ready = false;
    s.preparation_blocked_reasons = [reason];
    assert.equal(automationProgressStatus(s), '管理者のAI設定待ち');
    assert.match(automationProgressDescription(s), /進捗.*保持.*自動.*再開/);
    s.state = 'stopped';
    assert.equal(automationProgressStatus(s), '停止中');
    s.state = 'needs_confirmation';
    s.reason = 'account_changed';
    assert.equal(automationProgressStatus(s), '条件の再確認が必要');
  }
});

test('restored AI readiness shows existing progress without requiring matching AI scope versions', () => {
  const s = gettingStartedFixture().automation!;
  s.state = 'enabled';
  s.progress.bootstrap_complete = true;
  s.progress.historical_complete = true;
  s.progress.pending_count = 8;
  assert.equal(automationProgressStatus(s), '解析中');
  s.blocked_reasons = ['account_changed', 'runtime_inactive'];
  s.ready = false;
  assert.notEqual(automationProgressStatus(s), '管理者のAI設定待ち');
});
