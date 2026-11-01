import type { CandidateFilter } from './approval-api.ts';
import type { SubscriptionFilter } from './subscriptions-api.ts';

type ListLocation = {
  pathname: string;
  searchParams: Pick<URLSearchParams, 'get' | 'toString'>;
};

export const candidateSorts = [
  { value: 'received_desc', label: 'メール受信日（新しい順）' },
  { value: 'received_asc', label: 'メール受信日（古い順）' },
  { value: 'service', label: 'サービス名（昇順）' },
  { value: 'service_desc', label: 'サービス名（降順）' },
] as const;
export const subscriptionSorts = [
  { value: 'next_billing', label: '次回請求日（近い順）' },
  { value: 'next_billing_desc', label: '次回請求日（遠い順）' },
  { value: 'service', label: 'サービス名（昇順）' },
  { value: 'service_desc', label: 'サービス名（降順）' },
  { value: 'updated', label: '更新日（新しい順）' },
  { value: 'updated_asc', label: '更新日（古い順）' },
] as const;
function offset(url: ListLocation) {
  const value = Number(url.searchParams.get('offset') ?? 0);
  return Number.isSafeInteger(value) && value >= 0 ? value : 0;
}
function sort<T extends string>(
  url: ListLocation,
  options: readonly { value: T }[],
): T {
  return (
    options.find((item) => item.value === url.searchParams.get('sort'))
      ?.value ?? options[0].value
  );
}
export function candidateFilter(url: ListLocation): CandidateFilter {
  return {
    account_id: url.searchParams.get('account_id') ?? '',
    q: (url.searchParams.get('q') ?? '').trim(),
    sort: sort(url, candidateSorts),
    limit: 20,
    offset: offset(url),
  };
}
export function subscriptionFilter(url: ListLocation): SubscriptionFilter {
  const status = url.searchParams.get('status');
  return {
    account_id: url.searchParams.get('account_id') || undefined,
    q: (url.searchParams.get('q') ?? '').trim(),
    sort: sort(url, subscriptionSorts),
    status:
      status === 'active' || status === 'ended' || status === 'unknown'
        ? status
        : 'all',
    limit: 20,
    offset: offset(url),
  };
}
export function listURL(
  url: ListLocation,
  patch: Record<string, string | number | undefined> = {},
) {
  const params = new URLSearchParams(url.searchParams.toString());
  if (['q', 'sort', 'account_id', 'status'].some((key) => key in patch))
    params.set('offset', '0');
  for (const [key, value] of Object.entries(patch)) {
    if (value === undefined || value === '') params.delete(key);
    else params.set(key, String(value));
  }
  return `${url.pathname}?${params}`;
}
