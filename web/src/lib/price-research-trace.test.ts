import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { ApiError } from './api.ts';
import { createPriceResearchAPI } from './price-research-api.ts';
import { createResearchTraceController } from './price-research-trace-controller.ts';

const id = '11111111-1111-4111-8111-111111111111';
const now = '2026-11-05T00:00:00Z';
const trace = {
  call_id: id,
  captured_at: now,
  expires_at: now,
  document: {
    request: { prompt: '<img src=x onerror=alert(1)>' },
    response: '<script>alert(1)</script>',
    response_truncated: true,
    reference_prices: {},
    validation: { model: '<b>invalid</b>' },
  },
};
const call = {
  call_id: id,
  attempt_no: 1,
  status: 'completed',
  available: true,
  captured_at: now,
  expires_at: now,
};
const token = async () => 'token';

test('trace API fetches only dedicated administrator routes without caching', async () => {
  const requests: [string, RequestInit | undefined][] = [];
  const api = createPriceResearchAPI('', async (url, init) => {
    requests.push([String(url), init]);
    return Response.json(String(url).endsWith('/trace') ? trace : [call]);
  });
  assert.deepEqual(await api.traceCalls(id, token), [call]);
  assert.deepEqual(await api.trace(id, id, token), trace);
  assert.equal(requests[0][0], `/v1/admin/ai/price-research/runs/${id}/calls`);
  assert.equal(
    requests[1][0],
    `/v1/admin/ai/price-research/runs/${id}/calls/${id}/trace`,
  );
  assert.equal(requests[1][1]?.cache, 'no-store');
  assert.equal(requests[1][1]?.credentials, 'omit');
});

test('trace controller loads metadata explicitly, purges on denial and ignores late results after close', async () => {
  let finish: (value: typeof trace) => void = () => {};
  let denied = 0;
  let signal: AbortSignal | undefined;
  const controller = createResearchTraceController(
    {
      ...createPriceResearchAPI(''),
      traceCalls: async () => [call],
      trace: async (_run, _call, _token, requestSignal) => {
        signal = requestSignal;
        return new Promise((resolve) => {
          finish = resolve;
        });
      },
    },
    token,
    () => {},
    () => denied++,
  );
  await controller.load(id);
  assert.equal(controller.state.trace, null);
  const pending = controller.fetch(id, id);
  controller.close();
  finish(trace);
  await pending;
  assert.equal(signal?.aborted, true);
  assert.equal(controller.state.trace, null);
  const rejected = createResearchTraceController(
    {
      ...createPriceResearchAPI(''),
      traceCalls: async () => [call],
      trace: async () => {
        throw new ApiError('access_denied');
      },
    },
    token,
    () => {},
    () => denied++,
  );
  await rejected.load(id);
  await rejected.fetch(id, id);
  assert.equal(denied, 1);
  assert.deepEqual(rejected.state.calls, []);
  assert.equal(rejected.state.trace, null);
  await rejected.load(id);
  assert.deepEqual(rejected.state.calls, []);
});

test('trace document renders provider text as escaped text with truncation notice', async () => {
  const source = await readFile(
    new URL('./ResearchTraceDocument.svelte', import.meta.url),
    'utf8',
  );
  const compiled = compile(source, { generate: 'server' });
  assert.deepEqual(compiled.warnings, []);
  const code = compiled.js.code.replace(
    /(from\s+|import\s+)(['"])([^'"]+)\2/g,
    (_m, p, q, path) =>
      `${p}${q}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${q}`,
  );
  const TraceDocument = (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
  const html = render(TraceDocument, { props: { trace } }).body;
  assert.doesNotMatch(html, /<script>|<img|<b>/);
  assert.match(html, /&lt;script/);
  assert.match(html, /&lt;img/);
  assert.match(html, /切り詰め/);
  for (const label of ['送信内容', '実応答', '公式基準', '検証理由'])
    assert.ok(html.includes(label));
});
