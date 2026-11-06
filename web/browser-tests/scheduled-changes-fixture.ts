import type { Subscription } from '../src/lib/subscriptions-api.ts';
import {
  changePreview,
  changeEvidence,
} from '../src/lib/subscription-changes-fixtures.ts';

// Synthetic durable approval: the current contract never changes at reservation.
export function scheduledChangesFixture(mode: string, current: Subscription) {
  const base = `/synthetic/v1/subscriptions/${current.id}`;
  const sourceID = '66666666-6666-4666-8666-666666666666';
  const effective = '2040-12-01';
  const preview = {
    ...changePreview(),
    subscription: current,
    algorithm_version: 'subscription-change-v2',
    source: {
      ...changeEvidence(),
      result_id: sourceID,
      original_event: {
        ...changeEvidence().original_event,
        recurring_amount: '19',
        recurring_currency: current.currency,
        effective_date: effective,
      },
    },
    fields: [
      {
        key: 'amount',
        current: current.amount,
        proposed: '19',
        selectable: true,
        reason: '',
      },
    ],
    scheduled_for: effective,
  };
  const record = {
    id: '77777777-7777-4777-8777-777777777777',
    request_id: '88888888-8888-4888-8888-888888888888',
    subscription_id: current.id,
    version: 1,
    state: 'pending',
    effective_on: effective,
    base_version: current.version,
    preview,
    selected_fields: ['amount'],
    created_at: '2026-11-03T01:00:00Z',
    applied_at: null,
    reason: '',
  };
  let saved = mode === 'scheduled-list';
  let request: string | null = null;
  let attempts = 0;
  return (url: URL, method: string, body: Record<string, unknown> | null) => {
    if (method === 'GET' && url.pathname === base)
      return Response.json(current);
    if (
      method === 'POST' &&
      ['/change-preview', '/scheduled-change-preview'].some(
        (path) => url.pathname === base + path,
      )
    ) {
      if (
        body?.expected_version !== current.version ||
        body.source_result_id !== sourceID ||
        body.source_event_index !== 0
      )
        throw Error('Unexpected scheduled preview identity');
      if (url.pathname.endsWith('/scheduled-change-preview'))
        return Response.json(preview);
      const { scheduled_for: _date, ...normal } = preview;
      return Response.json({
        ...normal,
        fields: preview.fields.map((field) => ({
          ...field,
          selectable: false,
          reason: 'future_effective',
        })),
        warnings: [{ code: 'future_effective' }],
      });
    }
    if (method === 'POST' && url.pathname === base + '/scheduled-changes') {
      if (
        !body ||
        body.expected_version !== current.version ||
        body.source_result_id !== sourceID ||
        body.source_event_index !== 0 ||
        body.effective_on !== effective ||
        body.confirmed !== true ||
        body.preview_token !== preview.preview_token ||
        JSON.stringify(body.selected_fields) !== '["amount"]' ||
        typeof body.request_id !== 'string'
      )
        throw Error('Unconfirmed or mismatched scheduled change');
      const encoded = JSON.stringify(body);
      if (request && request !== encoded)
        throw Error('Uncertain reservation changed its confirmed request');
      request = encoded;
      record.request_id = body.request_id;
      saved = true;
      attempts++;
      if (mode === 'scheduled-uncertain' && attempts === 1)
        return new Response('{}', { status: 503 });
      return Response.json({ schedule: record, current });
    }
    if (method === 'GET' && url.pathname === base + '/scheduled-changes')
      return Response.json({
        schedules: saved ? [record] : [],
        total: saved ? 1 : 0,
        limit: 20,
        offset: 0,
      });
    if (
      method === 'POST' &&
      url.pathname === base + '/scheduled-changes/' + record.id + '/cancel'
    ) {
      if (
        body?.expected_version !== record.version ||
        body.confirmed !== true ||
        record.state !== 'pending'
      )
        throw Error('Unexpected reservation cancellation');
      record.state = 'cancelled';
      record.version++;
      return Response.json(record);
    }
    throw Error('Unexpected scheduled change endpoint');
  };
}
