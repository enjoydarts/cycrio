import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
import { gettingStartedFixture } from './getting-started-fixtures.ts';
import type { GettingStartedPage } from './getting-started.ts';
import type { FeedView } from './daily-controller.ts';

async function panel() {
  const source = await readFile(
    new URL('./GettingStartedPanel.svelte', import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const code = js.code.replace(
    /(from\s+|import\s*)(['"])([^'"]+)\2/g,
    (_match, prefix, quote, path) =>
      `${prefix}${quote}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${quote}`,
  );
  return (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
}
const ready = (): FeedView<GettingStartedPage> => ({
  page: gettingStartedFixture(),
  status: 'ready',
  error: null,
});
test('actual guide HTML scopes progress to the selected account and links to the next action', async () => {
  const Component = await panel(),
    view = ready();
  view.page!.automation!.state = 'enabled';
  view.page!.automation!.progress.pending_count = 8;
  const html = render(Component, { props: { view, admin: false } }).body;
  assert.match(html, /初回の案内/);
  assert.match(html, /メールを取り込み・解析しています/);
  assert.match(html, /個人のメール/);
  assert.match(html, /解析待ち/);
  assert.match(html, /8 件/);
  assert.match(html, /href="\/analysis\?account_id=/);
  assert.doesNotMatch(
    html,
    /<table\b|<aside\b|送信する<|取り込みを開始<|href="\/settings\/ai/,
  );
});
test('owner permission wait has no management link but admin gets the existing permission page', async () => {
  const Component = await panel(),
    view = ready();
  view.page!.preview!.full_email_allowed = false;
  const owner = render(Component, { props: { view, admin: false } }).body;
  assert.match(owner, /管理者による/);
  assert.doesNotMatch(owner, /href="\/settings\/ai/);
  const admin = render(Component, { props: { view, admin: true } }).body;
  assert.match(admin, /href="\/settings\/ai\?stage=permission"/);
});
test('loading, failed and denied reads never render stale readiness or progress as success', async () => {
  const Component = await panel();
  for (const status of ['loading', 'error', 'denied', 'disposed'] as const) {
    const view = {
      ...ready(),
      status,
      error: status === 'error' ? ('dependency_unavailable' as const) : null,
    };
    const html = render(Component, { props: { view, admin: true } }).body;
    assert.doesNotMatch(
      html,
      /準備ができています|href="\/analysis|個人のメール/,
    );
    if (status === 'error') {
      assert.match(html, /role="alert"/);
      assert.match(html, /もう一度確認/);
    }
  }
});
