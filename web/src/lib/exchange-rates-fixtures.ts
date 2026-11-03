export const ratesFixture = () => ({
  source: 'ECB' as const,
  source_url: 'https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml',
  as_of: '2026-11-02',
  fetched_at: '2026-11-03T00:00:00Z',
  base_currency: 'JPY' as const,
  rates: [
    { currency: 'JPY', rate: '1' },
    { currency: 'USD', rate: '150.123456789012' },
    { currency: 'EUR', rate: '170' },
  ],
});
