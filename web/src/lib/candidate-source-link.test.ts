import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { createApprovalAPI } from './approval-api.ts';
import {
  parseCandidateSourceURL,
  clearCandidateSourceURL,
  createCandidateSourceLink,
} from './candidate-source-link.ts';
import { changeEvidence } from './subscription-changes-fixtures.ts';
const source = {
  result_id: '66666666-6666-4666-8666-666666666666',
  event_index: 0,
};
const candidate = () => ({
  ...source,
  account_id: '11111111-1111-4111-8111-111111111111',
  account_label: '合成接続',
  received_at: null,
  event: changeEvidence().original_event,
  mail_count: 2,
  related_count: 2,
});
test('candidate source links validate UUID/index zero strictly and close consumes only source parameters', () => {
  const base = new URL(
    'https://example.test/analysis?tab=attention&account_id=kept',
  );
  assert.equal(parseCandidateSourceURL(base), null);
  const url = new URL(`${base}&result_id=${source.result_id}&event_index=0`);
  assert.deepEqual(parseCandidateSourceURL(url), source);
  assert.equal(
    clearCandidateSourceURL(url),
    '/analysis?tab=attention&account_id=kept',
  );
  for (const query of [
    'result_id=bad&event_index=0',
    `result_id=${source.result_id}`,
    `result_id=${source.result_id}&event_index=32`,
    `result_id=${source.result_id}&event_index=+0`,
    `result_id=${source.result_id}&event_index=0&event_index=1`,
  ])
    assert.throws(() =>
      parseCandidateSourceURL(
        new URL('https://example.test/analysis?' + query),
      ),
    );
});
test('direct source API requests one exact pending owner candidate without scanning list pages', async () => {
  const calls: string[] = [];
  const api = createApprovalAPI('', async (url) => {
    calls.push(String(url));
    return Response.json(candidate());
  });
  const row = await api.getCandidate(source, async () => 't');
  assert.equal(row.mail_count, 2);
  assert.deepEqual(calls, [
    `/v1/subscription-candidates/${source.result_id}/0`,
  ]);
  const bad = createApprovalAPI('', async () =>
    Response.json({ ...candidate(), event_index: 1 }),
  );
  await assert.rejects(bad.getCandidate(source, async () => 't'));
});
test('direct source controller treats stale results as missing and ignores late owner/source responses', async () => {
  let resolve!: (value: ReturnType<typeof candidate>) => void,
    denied = 0;
  const link = createCandidateSourceLink(
    {
      getCandidate: () =>
        new Promise((r) => {
          resolve = r;
        }),
    },
    async () => 't',
    () => {},
    () => denied++,
  );
  const pending = link.load(source);
  await link.load(null);
  resolve(candidate());
  await pending;
  assert.equal(link.state().candidate, null);
  const missing = createCandidateSourceLink(
    {
      getCandidate: async () => {
        throw new ApiError('not_found');
      },
    },
    async () => 't',
    () => {},
    () => denied++,
  );
  await missing.load(source);
  assert.equal(missing.state().status, 'missing');
  assert.equal(missing.state().candidate, null);
  const lost = createCandidateSourceLink(
    {
      getCandidate: async () => {
        throw new ApiError('access_denied');
      },
    },
    async () => 't',
    () => {},
    () => denied++,
  );
  await lost.load(source);
  assert.equal(denied, 1);
  assert.equal(lost.state().candidate, null);
});
