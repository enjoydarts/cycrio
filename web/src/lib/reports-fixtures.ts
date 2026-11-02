export function reportFixture(month = '2026-11') {
  const end = new Date(`${month}-01T00:00:00Z`);
  end.setUTCMonth(end.getUTCMonth() + 1);
  const start = new Date(end);
  start.setUTCMonth(start.getUTCMonth() - 12);
  return {
    as_of: '2026-11-02',
    generated_at: '2026-11-02T01:00:00Z',
    timezone: 'Asia/Tokyo',
    month,
    history_start: start.toISOString().slice(0, 10),
    history_end: end.toISOString().slice(0, 10),
    contracts: [],
    currencies: [],
    history: Array.from({ length: 12 }, (_, i) => {
      const date = new Date(start);
      date.setUTCMonth(date.getUTCMonth() + i);
      return { month: date.toISOString().slice(0, 7), currencies: [] };
    }),
    monthly_services: [],
    upcoming: [],
    coverage: {
      total_contracts: 0,
      open_contracts: 0,
      active_contracts: 0,
      unknown_contracts: 0,
      ended_contracts: 0,
      fixed_term_contracts: 0,
      normalized_contracts: 0,
      unpriced_contracts: 0,
      missing_date_contracts: 0,
      overdue_contracts: 0,
      actual_record_count: 0,
    },
  };
}
