import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
async function html(name: string, props: Record<string, unknown>) {
  const source = await readFile(
    new URL(`./${name}.svelte`, import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const code = js.code.replace(
    /(from\s+)(['"])([^'"]+)\2/g,
    (_m, p, q, path) =>
      `${p}${q}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${q}`,
  );
  return render(
    (
      await import(
        `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
      )
    ).default,
    { props },
  ).body;
}
test('candidate list exposes extracted price and a review action without technical processing failures', async () => {
  const body = await html('CandidatesPanel', {
    page: {
      candidates: [
        {
          result_id: 'result',
          event_index: 0,
          account_id: 'account',
          account_label: '個人メール',
          mail_count: 1,
          event: {
            service: 'Example',
            plan: 'Pro',
            event_type: 'payment',
            amount: '1200',
            currency: 'JPY',
            interval_unit: 'month',
            interval_count: 1,
          },
        },
      ],
      total: 101,
      limit: 20,
      offset: 0,
    },
    onSelect: () => {},
  });
  assert.match(body, /Example/);
  assert.match(body, /1,200|1200/);
  assert.match(body, /確認する/);
  assert.match(body, /不要/);
  assert.doesNotMatch(body, /解析失敗|自動登録待ち|メール件名|差出人/);
});
test('summary distinguishes email counts, candidate counts, and registered contracts', async () => {
  const body = await html('AccountImportSummary', {
    summary: {
      mail_count: 12000,
      pending_count: 3600,
      succeeded_count: 400,
      unrelated_count: 7900,
      failed_count: 90,
      unknown_count: 10,
      candidate_count: 18,
      subscription_count: 7,
    },
  });
  assert.match(body, /12,000/);
  assert.match(body, /取得済み/);
  assert.match(body, /確認待ち候補/);
  assert.match(body, /登録済みサブスク/);
  assert.match(body, /18/);
  assert.match(body, /7/);
  assert.match(body, /処理失敗/);
  assert.match(body, /結果不明/);
});
