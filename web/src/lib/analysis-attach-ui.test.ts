import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { compile } from 'svelte/compiler';

for (const name of ['AnalysisAttach', 'CandidateReview', 'AnalysisDetail']) {
  test(`${name} compiles without accessibility or component warnings`, async () => {
    const source = await readFile(
      new URL(`./${name}.svelte`, import.meta.url),
      'utf8',
    );
    for (const generate of ['client', 'server'] as const) {
      const { warnings } = compile(source, { generate });
      assert.deepEqual(warnings, []);
    }
  });
}
