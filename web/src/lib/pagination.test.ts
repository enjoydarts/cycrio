import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';

test('numbered pagination renders direct links and identifies the current page without an input', async () => {
  const { default: Pagination } = await import('./Pagination.svelte');
  const html = render(Pagination, {
    props: {
      total: 180,
      limit: 20,
      offset: 40,
      href: (offset: number) => `/subscriptions?status=active&offset=${offset}`,
    },
  }).body;
  assert.match(html, /<nav[^>]*aria-label="ページ切り替え"/);
  assert.match(html, /aria-current="page"/);
  assert.match(html, /aria-label="3ページ（現在のページ）"/);
  assert.match(html, /href="\/subscriptions\?status=active&amp;offset=20"/);
  assert.match(html, /href="\/subscriptions\?status=active&amp;offset=160"/);
  assert.doesNotMatch(html, /<input/);
});

test('pagination stays bounded for thousands of pages and exposes first, nearby and last pages', async () => {
  const { paginationState } = await import('./pagination.ts');
  assert.deepEqual(paginationState(20000, 9980, 20).items, [
    1,
    'ellipsis',
    499,
    500,
    501,
    'ellipsis',
    1000,
  ]);
  assert.deepEqual(paginationState(200, 0, 20).items, [
    1,
    2,
    3,
    4,
    'ellipsis',
    10,
  ]);
  assert.deepEqual(paginationState(200, 180, 20).items, [
    1,
    'ellipsis',
    7,
    8,
    9,
    10,
  ]);
  assert.deepEqual(paginationState(100, 40, 20).items, [1, 2, 3, 4, 5]);
  assert.deepEqual(paginationState(120, 60, 20).items, [1, 2, 3, 4, 5, 6]);
});

test('page bounds handle a partial final page, empty results and an obsolete offset', async () => {
  const { paginationState } = await import('./pagination.ts');
  assert.equal(paginationState(41, 40, 20).totalPages, 3);
  assert.equal(paginationState(41, 40, 20).current, 3);
  assert.equal(paginationState(0, 0, 20).totalPages, 1);
  const stale = paginationState(41, 200, 20);
  assert.equal(stale.current, 11);
  assert.deepEqual(stale.items, [1, 2, 3]);
  assert.equal(stale.previous, 3);
  for (const values of [
    [-1, 0, 20],
    [10, -1, 20],
    [10, 0, 0],
    [10, 0, 1.5],
  ]) {
    assert.throws(
      () => paginationState(...(values as [number, number, number])),
      RangeError,
    );
  }
});

test('empty and single pages hide navigation, while an obsolete offset offers recovery', async () => {
  const { default: Pagination } = await import('./Pagination.svelte');
  for (const total of [0, 1, 20]) {
    assert.doesNotMatch(
      render(Pagination, { props: { total, offset: 0, limit: 20 } }).body,
      /<nav/,
    );
  }
  const html = render(Pagination, {
    props: {
      total: 0,
      offset: 40,
      limit: 20,
      href: (offset: number) => `?offset=${offset}`,
    },
  }).body;
  assert.match(html, /href="\?offset=0"/);
  assert.doesNotMatch(html, /aria-current="page"/);
});

test('busy navigation removes every link destination and disables page buttons', async () => {
  const { default: Pagination } = await import('./Pagination.svelte');
  const html = render(Pagination, {
    props: {
      total: 120,
      offset: 20,
      limit: 20,
      disabled: true,
      href: (offset: number) => `?offset=${offset}`,
    },
  }).body;
  assert.doesNotMatch(html, /href=/);
  assert.match(html, /aria-disabled="true"/);
  assert.match(html, /<button[^>]*disabled/);
});

test('a stale offset smaller than one page still allows returning to the first page', async () => {
  const { default: Pagination } = await import('./Pagination.svelte');
  const html = render(Pagination, {
    props: {
      total: 5,
      offset: 10,
      limit: 20,
      href: (offset: number) => `?offset=${offset}`,
    },
  }).body;
  assert.match(html, /href="\?offset=0"/);
  assert.doesNotMatch(html, /aria-current="page"/);
});
