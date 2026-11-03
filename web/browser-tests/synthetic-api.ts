import type { Subscription } from '../src/lib/subscriptions-api.ts';

type Trace = {
  method: string;
  url: string;
  body: Record<string, unknown> | null;
};
declare global {
  interface Window {
    browserTest: {
      requests: Trace[];
      errors: string[];
      saved: Subscription | null;
    };
  }
}
export const resultID = '66666666-6666-4666-8666-666666666666';
const fail = (message: string): never => {
  throw Error(message);
};
const equal = (a: unknown, b: unknown) =>
  JSON.stringify(a) === JSON.stringify(b);
const keys = (body: Record<string, unknown>, expected: string[]) => {
  if (!equal(Object.keys(body).sort(), expected.sort()))
    fail('Unexpected payload fields: ' + JSON.stringify(body));
};
const response = (body: unknown) =>
  new Response(JSON.stringify(body), {
    headers: { 'Content-Type': 'application/json' },
  });

// This fake checks transport contracts and returns fixed synthetic records.
// Business parsing, normalization, and controller behavior run in real components.
export function installAPI(mode: string, initial: Subscription) {
  window.browserTest = { requests: [], errors: [], saved: null };
  let version = 1;
  const long = {
    ...initial,
    service: 'SyntheticServiceWithAVeryLongUnbrokenNameForLayoutVerification',
    plan: '長いプラン名を持つテスト契約・チーム向けスタンダード月額プラン',
  };
  window.fetch = async (resource, options) => {
    try {
      const url = new URL(String(resource), location.href);
      const method = options?.method ?? 'GET';
      const body = options?.body ? JSON.parse(String(options.body)) : null;
      window.browserTest.requests.push({
        method,
        url: url.pathname + url.search,
        body,
      });
      if (
        new Headers(options?.headers).get('Authorization') !==
        'Bearer synthetic'
      )
        fail('Unexpected authorization');
      if (
        url.origin !== location.origin ||
        !url.pathname.startsWith('/synthetic/v1/subscriptions')
      )
        fail('Unexpected API origin or path');
      if (
        mode === 'attach' &&
        method === 'GET' &&
        url.pathname === '/synthetic/v1/subscriptions'
      ) {
        const params = Object.fromEntries(url.searchParams);
        if (
          !equal(Object.keys(params).sort(), [
            'limit',
            'offset',
            'q',
            'sort',
            'status',
          ]) ||
          params.limit !== '20' ||
          !['0', '20'].includes(params.offset) ||
          !['', 'Example'].includes(params.q) ||
          params.status !== 'all' ||
          params.sort !== 'service'
        )
          fail('Unexpected list query ' + url.search);
        return response({
          subscriptions: [long],
          total: 24,
          limit: 20,
          offset: Number(params.offset),
        });
      }
      if (
        mode === 'attach' &&
        method === 'POST' &&
        url.pathname === `/synthetic/v1/subscriptions/${initial.id}/sources`
      ) {
        if (
          !equal(body, {
            expected_version: 1,
            source_result_id: resultID,
            source_event_index: 0,
            confirmed: true,
          })
        )
          fail('Unexpected attach payload');
        return response(long);
      }
      if (
        mode === 'name' &&
        method === 'PUT' &&
        url.pathname === `/synthetic/v1/subscriptions/${initial.id}/name`
      ) {
        keys(body, ['confirmed', 'display_name', 'expected_version']);
        if (
          !body.confirmed ||
          body.expected_version !== version ||
          body.display_name !== (version === 1 ? '仕事用のメール' : null)
        )
          fail('Unexpected rename payload');
        return response({
          ...initial,
          display_name: body.display_name,
          version: ++version,
        });
      }
      const editing = mode === 'edit';
      if (
        method !== (editing ? 'PUT' : 'POST') ||
        url.pathname !==
          '/synthetic/v1/subscriptions' + (editing ? `/${initial.id}` : '')
      )
        fail('Unexpected save route');
      const fields = [
        'contract_kind',
        'effective_date',
        'access_end_date',
        'service',
        'plan',
        'amount',
        'currency',
        'interval_unit',
        'interval_count',
        'next_billing_date',
        'next_billing_mode',
        'status',
        'confirmed',
      ];
      keys(body, [
        ...fields,
        ...(editing
          ? ['expected_version']
          : ['request_id', 'source_result_id', 'source_event_index']),
      ]);
      if (!body.confirmed || body.service !== 'Example Mail')
        fail('Save must be explicit');
      if (editing && body.expected_version !== 1) fail('Missing edit version');
      if (
        !editing &&
        (!/^[0-9a-f-]{36}$/.test(body.request_id) ||
          body.source_result_id !== null ||
          body.source_event_index !== null)
      )
        fail('Unexpected create identity');
      const expectedMoney = mode === 'register' ? [null, null] : ['9', 'USD'];
      if (!equal([body.amount, body.currency], expectedMoney))
        fail('Unexpected money pair');
      if (
        !equal(
          [body.interval_unit, body.interval_count],
          mode === 'register' || editing ? ['year', 1] : [null, null],
        )
      )
        fail('Unexpected optional cycle');
      return response({ ...initial, ...body, version: editing ? 2 : 1 });
    } catch (error) {
      window.browserTest.errors.push(String(error));
      throw error;
    }
  };
}
