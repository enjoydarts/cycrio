import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';

test('month selection uses explicit year and month dropdowns with navigation', async () => {
  const { default: MonthSelector } = await import('./MonthSelector.svelte');
  const html = render(MonthSelector, {
    props: { value: '2026-11', currentMonth: '2026-11', onchange: () => {} },
  }).body;
  assert.match(html, /<select[^>]*aria-label="集計年"/);
  assert.match(html, /<select[^>]*aria-label="集計月"/);
  assert.match(html, /前月/);
  assert.match(html, /翌月/);
  assert.match(html, /今月/);
  assert.match(html, /日本時間/);
  assert.doesNotMatch(html, /type="month"/);
});

test('month navigation crosses years and stops at supported boundaries', async () => {
  const { shiftMonth, replaceMonthPart } = await import('./month-selection.ts');
  assert.equal(shiftMonth('2026-01', -1), '2025-12');
  assert.equal(shiftMonth('2026-12', 1), '2027-01');
  assert.equal(shiftMonth('2000-01', -1), null);
  assert.equal(shiftMonth('2199-12', 1), null);
  assert.equal(shiftMonth('invalid', 1), null);
  assert.equal(replaceMonthPart('2026-11', 'year', '2025'), '2025-11');
  assert.equal(replaceMonthPart('2026-11', 'month', '02'), '2026-02');
  assert.equal(replaceMonthPart('2026-11', 'month', '13'), null);
  assert.equal(replaceMonthPart('2026-11', 'year', '1999'), null);
});

test('month controls disable navigation at limits and all controls during reconciliation', async () => {
  const { default: MonthSelector } = await import('./MonthSelector.svelte');
  for (const value of ['2000-01', '2199-12']) {
    const html = render(MonthSelector, {
      props: { value, currentMonth: '2026-11', onchange: () => {} },
    }).body;
    const label = value === '2000-01' ? '前月' : '翌月';
    assert.match(
      html.replace(/<!--[\s\S]*?-->/g, ''),
      new RegExp(`<button[^>]*disabled[^>]*>${label}</button>`),
    );
  }
  const html = render(MonthSelector, {
    props: {
      value: '2026-11',
      currentMonth: '2026-11',
      disabled: true,
      onchange: () => {},
    },
  }).body;
  assert.equal((html.match(/<select[^>]*disabled/g) ?? []).length, 2);
  assert.equal((html.match(/<button[^>]*disabled/g) ?? []).length, 3);
});
