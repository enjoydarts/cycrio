import assert from 'node:assert/strict';
import test from 'node:test';
import { cycle, money } from './daily-text.ts';
test('partly known AI cycles retain the known unit or count without inventing the missing half', () => {
  assert.equal(cycle('month', null), '回数不明・か月単位');
  assert.equal(cycle(null, 5), '単位不明・回数5');
  assert.equal(cycle(null, null), '周期不明');
  assert.equal(cycle('year', 1), '1年ごと');
});
test('money preserves textual precision and known halves including explicit zero', () => {
  assert.equal(
    money('123456789012345678901234.123456789012', 'JPY'),
    '123456789012345678901234.123456789012 JPY',
  );
  assert.equal(money('0', 'JPY'), '0 JPY');
  assert.equal(money(null, 'USD'), '金額不明 USD');
  assert.equal(money('1', null), '1 通貨不明');
});
