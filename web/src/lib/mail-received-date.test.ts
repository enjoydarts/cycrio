import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';
import { createApprovalAPI } from './approval-api.ts';
import {
  parseProcessingResult,
  type ProcessingEvent,
} from './mail-processing-api.ts';
import { parseSubscription } from './subscriptions-api.ts';
import {
  processingResult,
  processingIDs as ids,
} from './mail-processing-fixtures.ts';
import { subscription } from './subscriptions-fixtures.ts';

const event: ProcessingEvent = {
  service: 'Example',
  plan: null,
  event_type: 'payment',
  amount: null,
  currency: null,
  billed_at: null,
  period_start: null,
  period_end: null,
  next_billing_at: null,
  interval_unit: null,
  interval_count: null,
};
const candidate = {
  result_id: ids.result,
  event_index: 0,
  account_id: ids.account,
  account_label: '個人',
  event,
  mail_count: 1,
};
async function parseCandidate(patch: object) {
  const api = createApprovalAPI('', async () =>
    Response.json({
      candidates: [{ ...candidate, ...patch }],
      total: 1,
      limit: 20,
      offset: 0,
    }),
  );
  return (
    await api.candidates(
      { account_id: '', limit: 20, offset: 0 },
      async () => 't',
    )
  ).candidates[0];
}
test('received timestamp parsers preserve valid dates and normalize old or null fields without retaining email content', async () => {
  for (const received_at of [undefined, null, '2026-10-31T16:05:06Z']) {
    const patch = received_at === undefined ? {} : { received_at };
    const parsed = await parseCandidate({
      ...patch,
      subject: 'private',
      sender: 'private',
      body: 'private',
    });
    assert.equal(parsed.received_at, received_at ?? null);
    assert.equal(
      'subject' in parsed || 'sender' in parsed || 'body' in parsed,
      false,
    );
    const result = { ...processingResult(), ...patch };
    delete (result as { received_at?: unknown }).received_at;
    assert.equal(
      parseProcessingResult({ ...result, ...patch }).received_at,
      received_at ?? null,
    );
    const source = {
      result_id: ids.result,
      event_index: 0,
      original_event: event,
      ...patch,
    };
    assert.equal(
      parseSubscription({ ...subscription(), sources: [source] }).sources[0]
        .received_at,
      received_at ?? null,
    );
  }
});
test('received timestamp parsers reject malformed timestamps and impossible calendar dates', async () => {
  for (const received_at of [
    '',
    '2026-02-30T12:00:00Z',
    '2026-10-31',
    '2026-10-31T25:00:00Z',
    0,
  ]) {
    await assert.rejects(parseCandidate({ received_at }));
    assert.throws(() =>
      parseProcessingResult({ ...processingResult(), received_at }),
    );
    assert.throws(() =>
      parseSubscription({
        ...subscription(),
        sources: [
          {
            result_id: ids.result,
            event_index: 0,
            original_event: event,
            received_at,
          },
        ],
      }),
    );
  }
});
test('candidate list renders the received date in Japan time across a UTC date boundary and labels missing dates', async () => {
  const { default: CandidatesPanel } = await import('./CandidatesPanel.svelte');
  const page = {
    candidates: [
      { ...candidate, received_at: '2026-10-31T16:05:06Z' },
      { ...candidate, result_id: ids.message, received_at: null },
    ],
    total: 2,
    limit: 20,
    offset: 0,
  };
  const body = render(CandidatesPanel, {
    props: { page, onSelect: () => {} },
  }).body;
  assert.match(body, /メール受信日時/);
  assert.match(body, /2026\/11\/0?1 0?1:05/);
  assert.match(body, /日本時間/);
  assert.match(body, /受信日時未取得/);
  assert.doesNotMatch(body, /2026\/10\/31/);
});
test('processing results label email receipt separately from analysis completion and never substitute completion for missing receipt', async () => {
  const { default: MailProcessingResults } =
    await import('./MailProcessingResults.svelte');
  const body = render(MailProcessingResults, {
    props: {
      results: [
        processingResult({
          received_at: null,
          finished_at: '2026-10-31T16:05:06Z',
        }),
      ],
      canRetry: false,
      confirmationScope: 'test',
      onRetry: () => {},
    },
  }).body;
  assert.match(body, /メール受信日時：[\s\S]*受信日時未取得/);
});
