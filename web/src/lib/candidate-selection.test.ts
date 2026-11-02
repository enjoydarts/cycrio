import assert from 'node:assert/strict';
import test from 'node:test';
import {
  candidateSourceKey,
  createCandidateSelection,
} from './candidate-selection.ts';
const source = (n: number) => ({
  result_id: `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`,
  event_index: 0,
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
