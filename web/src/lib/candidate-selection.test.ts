import assert from 'node:assert/strict';
import test from 'node:test';
import {
  candidateSourceKey,
  createCandidateSelection,
  loadCandidateGroup,
} from './candidate-selection.ts';
const source = (n: number) => ({
  result_id: `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`,
  event_index: 0,
});
test('page deselection preserves selections from other pages and clear removes everything', () => {
  const selection = createCandidateSelection(source(1));
  selection.selectPage([source(2), source(3)]);
  selection.togglePage([source(2)], false);
  assert.deepEqual(selection.sources(), [source(1), source(3)]);
  selection.clear();
  assert.equal(selection.snapshot(), null);
});
test('all related sources are loaded before applying selection and groups over 100 are rejected', async () => {
  const offsets: number[] = [];
  const rows = Array.from({ length: 75 }, (_, i) => source(i + 1));
  const loaded = await loadCandidateGroup(async (offset) => {
    offsets.push(offset);
    return {
      candidates: rows.slice(offset, offset + 50),
      total: 75,
      offset,
      limit: 50,
    };
  }, new AbortController().signal);
  assert.deepEqual(offsets, [0, 50]);
  assert.deepEqual(loaded, rows);
  await assert.rejects(
    loadCandidateGroup(
      async () => ({
        candidates: rows.slice(0, 50),
        total: 101,
        offset: 0,
        limit: 50,
      }),
      new AbortController().signal,
    ),
  );
});
test('group selection never returns partial results after cancellation or a later page failure', async () => {
  const abort = new AbortController();
  await assert.rejects(
    loadCandidateGroup(async () => {
      abort.abort();
      return { candidates: [source(1)], total: 1, offset: 0, limit: 50 };
    }, abort.signal),
  );
  await assert.rejects(
    loadCandidateGroup(async (offset) => {
      if (offset > 0) throw new Error('unavailable');
      return {
        candidates: Array.from({ length: 50 }, (_, i) => source(i)),
        total: 51,
        offset,
        limit: 50,
      };
    }, new AbortController().signal),
    /unavailable/,
  );
});
test('all related selection rejects changing totals and missing rows', async () => {
  const rows = Array.from({ length: 80 }, (_, i) => source(i));
  for (const changedTotal of [false, true]) {
    await assert.rejects(() =>
      loadCandidateGroup(
        async (offset) => ({
          candidates: rows.slice(offset, offset + (offset ? 10 : 50)),
          total: changedTotal && offset ? 60 : 80,
          limit: 50,
          offset,
        }),
        new AbortController().signal,
      ),
    );
  }
});
test('selection starts with anchor only and keeps explicit page selections across pagination', () => {
  const selection = createCandidateSelection(source(1));
  assert.deepEqual(selection.sources(), [source(1)]);
  selection.selectPage([source(2), source(3)]);
  selection.toggle(source(1), false);
  assert.deepEqual(selection.sources(), [source(2), source(3)]);
  assert.deepEqual(selection.snapshot(), {
    ...source(2),
    additional_sources: [source(3)],
  });
  assert.notEqual(
    candidateSourceKey(selection.snapshot()!),
    candidateSourceKey(source(2)),
  );
});
test('selection rejects page additions above 100 without partially selecting and allows clearing', () => {
  const selection = createCandidateSelection(source(1));
  assert.equal(
    selection.selectPage(Array.from({ length: 100 }, (_, i) => source(i + 2))),
    false,
  );
  assert.equal(selection.sources().length, 1);
  selection.toggle(source(1), false);
  assert.equal(selection.snapshot(), null);
});
