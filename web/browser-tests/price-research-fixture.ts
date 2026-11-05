import { modelFixture } from '../src/lib/catalog-test-fixtures.ts';

export function priceResearchFixture() {
  const runID = '22222222-2222-4222-8222-222222222222';
  const callID = '33333333-3333-4333-8333-333333333333';
  const captured = '2026-11-05T00:00:00Z';
  const expires = '2026-11-12T00:00:00Z';
  const models = Array.from({ length: 121 }, (_, index) => ({
    ...modelFixture(),
    id: `11111111-1111-4111-8111-${String(index).padStart(12, '0')}`,
    provider: 'openai',
    model_id: `model-${index}`,
    display_name: `Research Model ${index}`,
  }));
  return (url: URL) => {
    if (url.pathname.endsWith(`/runs/${runID}/calls`)) {
      return Response.json([
        {
          call_id: callID,
          attempt_no: 1,
          status: 'completed',
          available: true,
          captured_at: captured,
          expires_at: expires,
        },
        {
          call_id: '44444444-4444-4444-8444-444444444444',
          attempt_no: 2,
          status: 'completed',
          available: false,
          captured_at: null,
          expires_at: null,
        },
      ]);
    }
    if (url.pathname.endsWith(`/runs/${runID}/calls/${callID}/trace`)) {
      return Response.json({
        call_id: callID,
        captured_at: captured,
        expires_at: expires,
        document: {
          request: {
            model: 'synthetic-model',
            input: [
              {
                role: 'user',
                content: '<img src=x onerror=alert(1)>synthetic-prompt',
                reference: `https://openai.com/${'long-reference-'.repeat(30)}`,
              },
            ],
          },
          response:
            '<script>synthetic-response</script>\n{"model":"synthetic-model","input":"unknown"}',
          response_truncated: false,
          reference_prices: {
            'synthetic-model': { input_per_million: '1.25', currency: 'USD' },
          },
          validation: { 'synthetic-model': 'official_price_mismatch' },
        },
      });
    }
    if (url.pathname.endsWith('/catalog')) {
      if (url.searchParams.get('limit') !== '100')
        throw Error('Expected all-candidate page size');
      const offset = Number(url.searchParams.get('offset'));
      return Response.json({
        models: models.slice(offset, offset + 100),
        total: models.length,
      });
    }
    if (!url.pathname.endsWith('/price-research'))
      throw Error('Unexpected research fixture path');
    return Response.json({
      version: 1,
      provider: 'openai',
      model_id: models[120].id,
      automatic_enabled: false,
      interval_hours: 24,
      next_run_at: null,
      ready: true,
      latest_run: {
        id: '22222222-2222-4222-8222-222222222222',
        kind: 'update',
        target_provider: 'openai',
        state: 'succeeded',
        error_code: '',
        created_at: '2026-11-01T00:00:00Z',
        started_at: '2026-11-01T00:00:00Z',
        finished_at: '2026-11-01T00:30:00Z',
        target_count: 121,
        completed_count: 121,
        updated_count: 100,
        unresolved_count: 2,
        non_text_count: 19,
        unclassified_count: 2,
        call_id: null,
        sources: ['https://openai.com/api/pricing/'],
      },
    });
  };
}
