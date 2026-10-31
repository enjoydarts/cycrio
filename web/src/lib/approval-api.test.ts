import assert from 'node:assert/strict';
import test from 'node:test';
import { createApprovalAPI } from './approval-api.ts';
import {
  processingIDs as ids,
  processingRun,
} from './mail-processing-fixtures.ts';
import { createMailProcessingAPI } from './mail-processing-api.ts';
const event = {
  service: 'Example',
  plan: null,
  event_type: 'payment',
  amount: '1200',
  currency: 'JPY',
  billed_at: null,
  period_start: null,
  period_end: null,
  next_billing_at: null,
  interval_unit: null,
  interval_count: null,
};
test('candidate parser retains only extracted facts and preserves server-wide total', async () => {
  const api = createApprovalAPI('', async () =>
    Response.json({
      candidates: [
        {
          result_id: ids.result,
          event_index: 0,
          account_id: ids.account,
          account_label: '個人',
          event,
          mail_count: 1,
          subject: 'must not keep',
          sender: 'must not keep',
        },
      ],
      total: 143,
      limit: 20,
      offset: 0,
    }),
  );
  const page = await api.candidates(
    { account_id: '', limit: 20, offset: 0 },
    async () => 't',
  );
  assert.equal(page.total, 143);
  assert.equal(page.candidates[0].event.amount, '1200');
  assert.equal('subject' in page.candidates[0], false);
  assert.equal('sender' in page.candidates[0], false);
});
test('account summary requires an explicit account and never fetches a global history', async () => {
  let called = 0;
  const api = createApprovalAPI('', async () => {
    called++;
    return Response.json({
      mail_count: 1200,
      pending_count: 100,
      succeeded_count: 50,
      unrelated_count: 1000,
      failed_count: 40,
      unknown_count: 10,
      candidate_count: 17,
      subscription_count: 3,
    });
  });
  await assert.rejects(api.summary('', async () => 't'));
  assert.equal(called, 0);
  const summary = await api.summary(ids.account, async () => 't');
  assert.equal(summary.mail_count, 1200);
  assert.equal(summary.candidate_count, 17);
});
test('dismissal sends only the confirmed opaque source reference', async () => {
  let body: unknown;
  const api = createApprovalAPI('', async (_url, init) => {
    body = JSON.parse(String(init?.body));
    return Response.json({});
  });
  await api.dismiss({ result_id: ids.result, event_index: 0 }, async () => 't');
  assert.deepEqual(body, {
    source_result_id: ids.result,
    source_event_index: 0,
    confirmed: true,
  });
});
test('bulk retry accepts a run containing more than 100 failed messages', async () => {
  const api = createMailProcessingAPI('', async () =>
    Response.json({
      run: processingRun({ max_messages: 1051, total_count: 1051 }),
      accepted_count: 1051,
      skipped_count: 2,
    }),
  );
  const result = await api.retryFailed(
    ids.account,
    {
      expected_version: 1,
      runtime_version: 1,
      profile_id: ids.profile,
      policy_version: 1,
      request_id: ids.request,
      max_messages: 100,
      confirmed: true,
      full_email: true,
    },
    async () => 't',
  );
  assert.equal(result.run.total_count, 1051);
  assert.equal(result.accepted_count, 1051);
  assert.equal(result.skipped_count, 2);
});
test('bulk retry binds the selected execution instead of every failure in the account', async () => {
  let body: Record<string, unknown> = {};
  const api = createMailProcessingAPI('', async (_url, init) => {
    body = JSON.parse(String(init?.body));
    return Response.json({
      run: processingRun(),
      accepted_count: 1,
      skipped_count: 0,
    });
  });
  const history_id = `automation:${ids.run}:2`;
  await api.retryFailed(
    ids.account,
    {
      expected_version: 1,
      runtime_version: 1,
      profile_id: ids.profile,
      policy_version: 1,
      request_id: ids.request,
      max_messages: 100,
      confirmed: true,
      full_email: true,
      history_id,
    },
    async () => 't',
  );
  assert.equal(body.history_id, history_id);
});
