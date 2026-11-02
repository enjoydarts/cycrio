import assert from 'node:assert/strict';
import test from 'node:test';
import { loadCandidateListSelection } from './candidate-list-selection.ts';
import type { Candidate } from './approval-api.ts';
const rows = Array.from(
  { length: 121 },
  (_, n) => ({ result_id: String(n), event_index: 0 }) as Candidate,
);
test('all-page selection only publishes a complete unchanged candidate snapshot', async () => {
  const signal = new AbortController().signal;
  const selected = await loadCandidateListSelection(
    async (offset) => ({
      candidates: rows.slice(offset, offset + 100),
      limit: 100,
      offset,
      total: 121,
    }),
    signal,
  );
  assert.equal(selected.length, 121);
  await assert.rejects(
    () =>
      loadCandidateListSelection(
        async (offset) => ({
          candidates: rows.slice(offset, offset + 100),
          limit: 100,
          offset,
          total: offset ? 122 : 121,
        }),
        signal,
      ),
    { code: 'conflict' },
  );
});
test('cancelled list selection ignores late pages', async () => {
  const abort = new AbortController();
  await assert.rejects(() =>
    loadCandidateListSelection(async (offset) => {
      abort.abort();
      return { candidates: rows, limit: 100, offset, total: 121 };
    }, abort.signal),
  );
});
