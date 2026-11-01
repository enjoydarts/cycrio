import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';
import { compile } from 'svelte/compiler';
import { readFile } from 'node:fs/promises';
import { createApprovalAPI } from './approval-api.ts';
import { createSubscriptionsAPI } from './subscriptions-api.ts';

test('unsubmitted draft survives parent filter replacement until committed query changes', async () => {
  // Compile the production script without its DOM, exposing only draft edits/readback.
  const source = await readFile(
    new URL('./ListSearchSort.svelte', import.meta.url),
    'utf8',
  );
  const script =
    source
      .slice(0, source.indexOf('</script>'))
      .replace('$props.id()', "'test-id'")
      .replace(
        '  function submit(event: SubmitEvent)',
        '  export function editDraft(value: string) { draft = value; }\n  export function readDraft() { return draft; }\n  function submit(event: SubmitEvent)',
      ) + '</script>';
  const { js, warnings } = compile(script, { generate: 'client' });
  assert.deepEqual(warnings, []);
  const code = js.code.replace(
    /(from\s+|import\s+)(['"])([^'"]+)\2/g,
    (_match, prefix, quote, path) =>
      `${prefix}${quote}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${quote}`,
  );
  const Component = (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
  const runtime = await import(import.meta.resolve('svelte/internal/client'));
  const filter = runtime.mutable_source({ q: 'Spotify', sort: 'service_desc' });
  let form: { editDraft: (value: string) => void; readDraft: () => string };
  const dispose = runtime.effect_root(() => {
    form = Component(null, {
      get q() {
        return runtime.get(filter).q;
      },
      sort: 'service_desc',
      options: [],
      onSearch: () => {},
      onSort: () => {},
      onClear: () => {},
    });
  });
  try {
    await runtime.flush();
    form!.editDraft('unsent');
    runtime.set(filter, { q: 'Spotify', sort: 'service' });
    await runtime.flush();
    assert.equal(form!.readDraft(), 'unsent');
    runtime.set(filter, { q: 'Netflix', sort: 'service' });
    await runtime.flush();
    assert.equal(form!.readDraft(), 'Netflix');
  } finally {
    dispose();
  }
});

test('candidate search encodes literal Unicode and reserved characters with explicit ordering', async () => {
  let request = '';
  const api = createApprovalAPI('', async (url) => {
    request = String(url);
    return Response.json({ candidates: [], total: 0, limit: 20, offset: 40 });
  });
  await api.candidates(
    {
      account_id: '',
      q: '  日本 %_&+  ',
      sort: 'service_desc',
      limit: 20,
      offset: 40,
    },
    async () => 'token',
  );
  const params = new URL(request, 'https://test').searchParams;
  assert.equal(params.get('q'), '日本 %_&+');
  assert.equal(params.get('sort'), 'service_desc');
  assert.equal(params.get('offset'), '40');
  assert.equal(params.has('account_id'), false);
});

test('candidate legacy filters receive defaults and invalid search never reaches the server', async () => {
  let called = 0;
  const api = createApprovalAPI('', async (url) => {
    called++;
    const params = new URL(String(url), 'https://test').searchParams;
    assert.equal(params.get('q'), '');
    assert.equal(params.get('sort'), 'received_desc');
    return Response.json({ candidates: [], total: 0, limit: 20, offset: 0 });
  });
  const filter = { account_id: '', limit: 20, offset: 0 };
  await api.candidates(filter, async () => 'token');
  for (const q of ['あ'.repeat(201), 'bad\u0000']) {
    await assert.rejects(
      api.candidates({ ...filter, q }, async () => 'token'),
      { code: 'invalid_request' },
    );
  }
  assert.equal(called, 1);
});

test('subscription API accepts every ascending and descending sort', async () => {
  const api = createSubscriptionsAPI('', async () =>
    Response.json({ subscriptions: [], total: 0, limit: 20, offset: 0 }),
  );
  for (const sort of [
    'next_billing',
    'next_billing_desc',
    'service',
    'service_desc',
    'updated',
    'updated_asc',
  ] as const) {
    await api.list(
      { q: '', status: 'all', sort, limit: 20, offset: 0 },
      async () => 'token',
    );
  }
});

test('URL filters retain search, ordering, account and status across pagination and reset changed conditions', async () => {
  const { candidateFilter, subscriptionFilter, listURL } =
    await import('./list-search-sort.ts');
  const url = new URL(
    'https://test/analysis?tab=attention&q=日本%20%25_&sort=service_desc&account_id=account&offset=40',
  );
  assert.deepEqual(candidateFilter(url), {
    q: '日本 %_',
    sort: 'service_desc',
    account_id: 'account',
    limit: 20,
    offset: 40,
  });
  const next = new URL(listURL(url, { offset: 60 }), url);
  assert.equal(next.searchParams.get('q'), '日本 %_');
  assert.equal(next.searchParams.get('sort'), 'service_desc');
  assert.equal(next.searchParams.get('account_id'), 'account');
  assert.equal(next.searchParams.get('tab'), 'attention');
  for (const patch of [
    { q: 'next' },
    { sort: 'received_asc' },
    { account_id: 'other' },
  ]) {
    assert.equal(
      new URL(listURL(url, patch), url).searchParams.get('offset'),
      '0',
    );
  }
  assert.equal(
    subscriptionFilter(
      new URL(
        'https://test/subscriptions?sort=updated_asc&status=ended&offset=20',
      ),
    ).sort,
    'updated_asc',
  );
  assert.equal(
    candidateFilter(
      new URL('https://test/analysis?sort=invalid&offset=Infinity'),
    ).sort,
    'received_desc',
  );
  assert.equal(
    candidateFilter(new URL('https://test/analysis?offset=9007199254740992'))
      .offset,
    0,
  );
});

test('shared search form renders visible labels, explicit button types and complete ordering choices', async () => {
  const { default: ListSearchSort } = await import('./ListSearchSort.svelte');
  const html = render(ListSearchSort, {
    props: {
      q: 'Example',
      sort: 'service_desc',
      options: [{ value: 'service_desc', label: 'サービス名（降順）' }],
      onSearch: () => {},
      onSort: () => {},
      onClear: () => {},
    },
  }).body;
  assert.match(html, /サービス・プランを検索/);
  assert.match(html, /並び順/);
  assert.match(html, /type="search"/);
  assert.match(html, /value="Example"/);
  assert.match(html, /type="submit"/);
  assert.match(html, /条件を解除/);
  assert.match(html, /サービス名（降順）/);
});

test('empty filtered candidates offer condition reset rather than claiming no contracts were found', async () => {
  const { default: CandidatesPanel } = await import('./CandidatesPanel.svelte');
  const html = render(CandidatesPanel, {
    props: {
      page: { candidates: [], total: 0, limit: 20, offset: 0 },
      filtered: true,
      onSelect: () => {},
      onClear: () => {},
    },
  }).body;
  assert.match(html, /条件に一致する候補はありません/);
  assert.match(html, /条件を解除する/);
  assert.doesNotMatch(html, /メールから契約情報が見つかると/);
});
