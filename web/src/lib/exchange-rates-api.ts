import type { TokenProvider } from './api.ts';
import {
  array,
  choice,
  createDailyRequest,
  date,
  invalid,
  record,
  timestamp,
} from './daily-api.ts';
import { createFeed, type FeedView } from './daily-controller.ts';

export const exchangeSourceURL =
  'https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml';
export type ExchangeRates = {
  source: 'ECB';
  source_url: string;
  as_of: string;
  fetched_at: string;
  base_currency: 'JPY';
  rates: { currency: string; rate: string }[];
};
export function parseExchangeRates(value: unknown): ExchangeRates {
  const v = record(value);
  if (v.source_url !== exchangeSourceURL) return invalid();
  const rates = array(
    v.rates,
    (x) => {
      const row = record(x);
      if (
        typeof row.rate !== 'string' ||
        !/^\d{1,24}(\.\d{1,12})?$/.test(row.rate) ||
        !/[1-9]/.test(row.rate)
      )
        return invalid();
      return {
        currency: choice(row.currency, ['JPY', 'USD', 'EUR']),
        rate: row.rate,
      };
    },
    3,
  );
  if (
    rates.length !== 3 ||
    new Set(rates.map((x) => x.currency)).size !== 3 ||
    rates.find((x) => x.currency === 'JPY')?.rate !== '1'
  )
    return invalid();
  return {
    source: choice(v.source, ['ECB']),
    source_url: exchangeSourceURL,
    as_of: date(v.as_of),
    fetched_at: timestamp(v.fetched_at),
    base_currency: choice(v.base_currency, ['JPY']),
    rates,
  };
}
export function createExchangeRatesAPI(
  base: string,
  fetcher: typeof fetch = fetch,
) {
  const request = createDailyRequest(base, fetcher);
  return {
    async read(
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<ExchangeRates> {
      return parseExchangeRates(
        await request('/v1/exchange-rates', token, signal),
      );
    },
  };
}
export function createExchangeRatesController(
  api: Pick<ReturnType<typeof createExchangeRatesAPI>, 'read'>,
  token: TokenProvider,
  changed: (v: FeedView<ExchangeRates>) => void,
  denied: () => void,
) {
  const feed = createFeed<ExchangeRates, undefined>(
    (_, token, signal) => api.read(token, signal),
    token,
    (v) => changed({ ...v, page: v.status === 'ready' ? v.page : null }),
    denied,
  );
  return {
    load: () => feed.load(undefined),
    state: () => {
      const v = feed.state();
      return { ...v, page: v.status === 'ready' ? v.page : null };
    },
    dispose: () => feed.dispose(),
  };
}
