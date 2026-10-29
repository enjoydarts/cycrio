import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
import { overviewFixture } from './overview-fixtures.ts';
const moduleURL = (code: string) =>
  `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
async function componentURL(name: string): Promise<string> {
  const source = await readFile(
    new URL(`./${name}.svelte`, import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const imports: Record<string, string> = {
    '$app/state': moduleURL(
      "export const page={url:new URL('https://test/analysis?tab=attention')};",
    ),
    '$app/navigation': moduleURL(
      'export const goto=()=>{};export const beforeNavigate=()=>{};',
    ),
    '$app/env/public': moduleURL(
      "export const PUBLIC_API_BASE_URL='https://synthetic.invalid';",
    ),
    './app-context.ts': moduleURL(
      'export const getAppSession=()=>({token:async()=>null,onDenied:()=>{},mailAPI:{list:async()=>[]}});',
    ),
  };
  for (const match of js.code.matchAll(
    /from\s+['"]\.\/([^'"]+)\.svelte['"]/g,
  )) {
    imports[`./${match[1]}.svelte`] =
      name === 'AnalysisInbox' &&
      !['LibraryHeader', 'LibraryPanel'].includes(match[1])
        ? moduleURL('export default function InactiveChild(){}')
        : await componentURL(match[1]);
  }
  const code = js.code
    .replace(/^import\s+['"][^'"]+\.css['"];?\n/gm, '')
    .replace(
      /(^import[\s\S]*?from\s+|^import\s*)(['"])([^'"]+)\2/gm,
      (_m, p, q, path) =>
        `${p}${q}${imports[path] ?? (path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path))}${q}`,
    );
  return moduleURL(code);
}
function visible(html: string) {
  let depth = 0;
  return html
    .split(/(<\/?details\b[^>]*>)/g)
    .filter((part) => {
      if (/^<details\b/.test(part)) {
        depth++;
        return false;
      }
      if (/^<\/details/.test(part)) {
        depth--;
        return false;
      }
      return depth === 0;
    })
    .join('')
    .replace(/<!--[\s\S]*?-->/g, '');
}
async function html(name: string, props: Record<string, unknown> = {}) {
  return render((await import(await componentURL(name))).default, { props })
    .body;
}
function panelHTML(body: string) {
  const start =
    /<(div|section)\b[^>]*class="[^"]*\blibrary-panel\b[^"]*"[^>]*>/.exec(body);
  assert.ok(start, 'screen uses the shared library panel');
  const offset = start.index + start[0].length;
  let depth = 1;
  for (const token of body
    .slice(offset)
    .matchAll(new RegExp(`<\\/?${start[1]}\\b[^>]*>`, 'g'))) {
    depth += token[0].startsWith('</') ? -1 : 1;
    if (depth === 0) return body.slice(offset, offset + token.index);
  }
  assert.fail('panel closing element missing');
}
test('daily screens share the subscription header and keep their primary filters and results in its panel', async () => {
  for (const name of ['AnalysisInbox', 'OverviewPanel', 'BillingSummary']) {
    const body = await html(
      name,
      name === 'OverviewPanel'
        ? { view: { page: overviewFixture(), status: 'ready', error: null } }
        : {},
    );
    assert.match(body, /<header[^>]*class="[^"]*\blibrary-header\b/);
    const panel = panelHTML(body);
    if (name === 'AnalysisInbox') {
      assert.match(panel, /解析結果の表示/);
      assert.match(panel, /すべてのアカウント/);
    } else if (name === 'OverviewPanel') {
      assert.match(panel, /保存済みの次回請求日/);
      assert.match(panel, /日常のサービス/);
    } else {
      assert.match(panel, /type="month"/);
      assert.match(panel, /保存済みの明細を集計/);
    }
  }
});
test('home leads with saved dates while supplementary billing, connection and population details stay reachable', async () => {
  const body = await html('OverviewPanel', {
      view: { page: overviewFixture(), status: 'ready', error: null },
    }),
    first = visible(body);
  assert.match(first, /日常のサービス/);
  assert.match(first, /金額不明/);
  assert.match(first, /0 JPY/);
  assert.match(first, /保存済みの次回請求日/);
  assert.match(first, /href="\/analysis\?tab=attention"/);
  assert.doesNotMatch(
    first,
    /メール連携と取り込み|支払・返金実績|対象 .*最大10件|請求確定/,
  );
  assert.match(body, /支払・返金実績/);
  assert.match(body, /個人メール/);
  assert.match(body, /123456789012345678901234.123456789012/);
  assert.equal((first.match(/class="[^\"]*primary/g) || []).length, 1);
});
test('monthly results keep exact money and counts together while calculation explanations are optional', async () => {
  const body = await html('BillingSummaryTable', {
      summary: overviewFixture().billing_month,
    }),
    first = visible(body);
  assert.match(first, /123456789012345678901234.123456789012/);
  assert.match(first, /JPY/);
  assert.match(first, /USD/);
  assert.doesNotMatch(first, /以上|金額集計の対象外|通貨の換算/);
  assert.equal((first.match(/<th(?:\s|>)/g) || []).length, 4);
  assert.match(body, /金額集計の対象外/);
  assert.match(body, /未満/);
});
test('analysis places results before its single management disclosure', async () => {
  const body = await html('AnalysisInbox');
  assert.ok(body.indexOf('取り込みの管理') > body.indexOf('解析結果の表示'));
  assert.doesNotMatch(
    visible(body),
    /件数を指定して手動解析|取り込みの状態・停止/,
  );
  assert.match(body, /メールを取り込む/);
});
test('history rows retain visible failures and stop actions with optional run breakdowns', async () => {
  const body = await html('AnalysisHistoryPanel', {
      page: {
        runs: [
          {
            id: 'run',
            state: 'running',
            account_label: 'Personal',
            created_at: '2026-10-29T00:00:00Z',
            completed_count: 4,
            total_count: 9,
            max_messages: 20,
            related_count: 1,
            unrelated_count: 0,
            failed_count: 1,
            unknown_count: 2,
          },
        ],
        total: 1,
        limit: 20,
        offset: 0,
      },
      onCancel: () => {},
    }),
    first = visible(body);
  assert.match(first, /Personal/);
  assert.match(first, /停止/);
  assert.match(first, /結果不明 2/);
  assert.match(first, /失敗 1/);
  assert.doesNotMatch(first, /指定上限|非関連/);
  assert.match(body, /指定上限/);
  const unknownOnly = visible(
    await html('AnalysisHistoryPanel', {
      page: {
        runs: [
          {
            id: 'run',
            state: 'completed',
            account_label: 'Personal',
            created_at: '2026-10-29T00:00:00Z',
            completed_count: 2,
            total_count: 2,
            max_messages: 2,
            related_count: 0,
            unrelated_count: 0,
            failed_count: 0,
            unknown_count: 2,
          },
        ],
        total: 1,
        limit: 20,
        offset: 0,
      },
      onCancel: () => {},
    }),
  );
  assert.doesNotMatch(unknownOnly, />\s*·\s*結果不明/);
});
