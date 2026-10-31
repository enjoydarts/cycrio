import type { Breakdown, Totals } from './airuntime-api';

// Scale integer coefficients before adding; never convert costs to Number.
function addDecimal(left: string, right: string): string {
  const [leftWhole, leftFraction = ''] = left.split('.');
  const [rightWhole, rightFraction = ''] = right.split('.');
  const scale = Math.max(leftFraction.length, rightFraction.length);
  const sum =
    BigInt(leftWhole + leftFraction.padEnd(scale, '0')) +
    BigInt(rightWhole + rightFraction.padEnd(scale, '0'));
  if (!scale) return sum.toString();
  const digits = sum.toString().padStart(scale + 1, '0');
  const fraction = digits.slice(-scale).replace(/0+$/, '');
  return digits.slice(0, -scale) + (fraction ? `.${fraction}` : '');
}

export function usageCounts(totals: Totals[]) {
  return totals.reduce(
    (counts, total) => ({
      calls: counts.calls + total.calls,
      failures: counts.failures + total.failures,
    }),
    { calls: 0, failures: 0 },
  );
}

export function providerTotals(breakdown: Breakdown[]) {
  const providers = new Map<
    Breakdown['provider'],
    {
      provider: Breakdown['provider'];
      totals: Totals[];
      calls: number;
      failures: number;
    }
  >();
  for (const row of breakdown) {
    let group = providers.get(row.provider);
    if (!group) {
      group = { provider: row.provider, totals: [], calls: 0, failures: 0 };
      providers.set(row.provider, group);
    }
    const counts = usageCounts(row.totals);
    group.calls += counts.calls;
    group.failures += counts.failures;
    for (const total of row.totals) {
      if (!total.currency || total.calls <= total.unknown_calls) continue;
      const existing = group.totals.find(
        (item) => item.currency === total.currency,
      );
      if (!existing) {
        group.totals.push({ ...total });
        continue;
      }
      existing.amount = addDecimal(existing.amount, total.amount);
      existing.estimated_amount = addDecimal(
        existing.estimated_amount,
        total.estimated_amount,
      );
      existing.reported_amount = addDecimal(
        existing.reported_amount,
        total.reported_amount,
      );
      existing.calls += total.calls;
      existing.unknown_calls += total.unknown_calls;
      existing.failures += total.failures;
    }
  }
  return Array.from(providers.values());
}
