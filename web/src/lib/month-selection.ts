const validMonth = /^(20\d{2}|21\d{2})-(0[1-9]|1[0-2])$/;

export function shiftMonth(value: string, direction: -1 | 1): string | null {
  if (!validMonth.test(value)) return null;
  const [year, month] = value.split('-').map(Number);
  const index = year * 12 + month - 1 + direction;
  const result = `${Math.floor(index / 12)}-${String((index % 12) + 1).padStart(2, '0')}`;
  return validMonth.test(result) ? result : null;
}

export function replaceMonthPart(
  value: string,
  part: 'year' | 'month',
  selection: string,
): string | null {
  if (!validMonth.test(value)) return null;
  const [year, month] = value.split('-');
  const result =
    part === 'year' ? `${selection}-${month}` : `${year}-${selection}`;
  return validMonth.test(result) ? result : null;
}
