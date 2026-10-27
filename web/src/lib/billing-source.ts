import type { TokenProvider } from './api.ts';
import type { BillingAPI } from './billing-api.ts';
export async function findBillingSource(
  api: Pick<BillingAPI, 'list'>,
  sid: string,
  source: { result_id: string; event_index: number },
  token: TokenProvider,
  signal: AbortSignal,
) {
  let offset = 0;
  while (!signal.aborted) {
    const page = await api.list(
      sid,
      {
        kind: 'all',
        state: 'all',
        month: '',
        source_result_id: source.result_id,
        source_event_index: source.event_index,
        limit: 20,
        offset,
      },
      token,
      signal,
    );
    if (signal.aborted) return null;
    const found = page.events.find(
      (e) =>
        e.subscription_id === sid &&
        e.sources.some(
          (s) =>
            s.result_id === source.result_id &&
            s.event_index === source.event_index,
        ),
    );
    if (found) return found;
    offset += page.limit;
    if (offset >= page.total) return null;
  }
  return null;
}
