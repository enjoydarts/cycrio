import type { Subscription } from '../src/lib/subscriptions-api.ts';
import { budgetFixture } from './budget-fixture.ts';
import { priceResearchFixture } from './price-research-fixture.ts';
import { notificationFixture } from './notification-fixture.ts';
import { erasureFixture } from './erasure-fixture.ts';
import { integrationProfileFixture } from './integration-profile-fixture.ts';
import { scheduledChangesFixture } from './scheduled-changes-fixture.ts';
import { healthchecksFixture } from './healthchecks-fixture.ts';
import { changeEvidence } from '../src/lib/subscription-changes-fixtures.ts';

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
      back?: boolean;
      signedOut?: boolean;
      ownerGeneration?: number;
      switchNotificationOwner?: () => void;
      releaseOldNotification?: () => void;
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
  const healthchecks =
    mode === 'healthchecks' || mode === 'posthog'
      ? healthchecksFixture(mode)
      : null;
  const research = mode === 'price-research' ? priceResearchFixture() : null;
  const scheduled = mode.startsWith('scheduled-')
    ? scheduledChangesFixture(mode, initial)
    : null;
  const integration =
    mode === 'integration-profile' ? integrationProfileFixture() : null;
  const erasure = mode.startsWith('erasure') ? erasureFixture(mode) : null;
  const budgets = mode.startsWith('budget') ? budgetFixture(mode) : null;
  window.browserTest = { requests: [], errors: [], saved: null };
  const notifications = mode.startsWith('notifications')
    ? notificationFixture(mode)
    : null;
  let version = 1;
  let attempts = 0,
    applied: unknown = null,
    firstChangeBody: unknown = null;
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
      if (research && url.origin === location.origin) return research(url);
      if (healthchecks && url.origin === location.origin)
        return healthchecks(url, method, body);
      if (scheduled && url.origin === location.origin)
        return scheduled(url, method, body);
      if (erasure && url.origin === location.origin)
        return erasure(url, method, body);
      if (integration && url.origin === location.origin)
        return integration(url, method, body);
      if (
        budgets &&
        url.origin === location.origin &&
        url.pathname.startsWith('/synthetic/v1/admin/ai/')
      )
        return budgets(url, method, body);
      if (
        url.origin !== location.origin ||
        !(
          url.pathname.startsWith('/synthetic/v1/subscriptions') ||
          (notifications &&
            (url.pathname.startsWith('/synthetic/v1/notifications') ||
              url.pathname === '/synthetic/v1/notification-settings' ||
              url.pathname.startsWith(
                '/synthetic/v1/subscription-candidates/',
              )))
        )
      )
        fail('Unexpected API origin or path');
      if (notifications) return notifications(url, method, body);
      if (mode.startsWith('change')) {
        const path = `/synthetic/v1/subscriptions/${initial.id}`;
        const before = {
          ...initial,
          version,
          amount: version === 1 ? '9' : '11',
        };
        const source = {
          ...changeEvidence(),
          result_id: resultID,
          original_event: {
            ...changeEvidence().original_event,
            recurring_amount: '19',
            recurring_currency: 'USD',
            interval_unit: 'year',
            interval_count: 1,
            next_billing_at: '2027-12-01',
            ...(mode === 'change-lifecycle'
              ? { auto_renew: false, trial_end_date: '2028-02-29' }
              : {}),
          },
        };
        if (method === 'GET' && url.pathname === '/synthetic/v1/subscriptions')
          return response({
            subscriptions: [initial],
            total: 1,
            limit: 20,
            offset: 0,
          });
        if (method === 'GET' && url.pathname === path) return response(before);
        if (method === 'POST' && url.pathname === path + '/change-preview') {
          if (mode === 'change-source-conflict' && attempts > 0)
            return new Response('{}', { status: 409 });
          if (
            !equal(body, {
              expected_version: version,
              source_result_id: resultID,
              source_event_index: 0,
            })
          )
            fail('Unexpected preview identity');
          return response({
            subscription: before,
            source,
            additional_evidence: [],
            preview_token: (version === 1 ? 'a' : 'b').repeat(64),
            as_of: '2026-11-03',
            timezone: 'Asia/Tokyo',
            algorithm_version: 'subscription-change-v1',
            fields: [
              ...(mode === 'change-lifecycle'
                ? [
                    {
                      key: 'auto_renew',
                      current: true,
                      proposed: false,
                      selectable: true,
                      reason: '',
                    },
                  ]
                : []),
              {
                key: 'amount',
                current: before.amount,
                proposed: '19',
                selectable: true,
                reason: '',
              },
              {
                key: 'interval',
                current: { unit: 'month', count: 1 },
                proposed: { unit: 'year', count: 1 },
                selectable: true,
                reason: '',
              },
              {
                key: 'next_billing_date',
                current: '2027-11-01',
                proposed: '2027-12-01',
                selectable: true,
                reason: '',
              },
            ],
            warnings: [],
            derived_next_billing_date: null,
          });
        }
        if (method === 'POST' && url.pathname === path + '/changes') {
          attempts++;
          keys(body, [
            'expected_version',
            'source_result_id',
            'source_event_index',
            'request_id',
            'preview_token',
            'selected_fields',
            'confirmed',
          ]);
          if (
            body.expected_version !== version ||
            body.source_result_id !== resultID ||
            body.source_event_index !== 0 ||
            body.preview_token !== (version === 1 ? 'a' : 'b').repeat(64) ||
            !body.confirmed ||
            !equal(body.selected_fields, [
              mode === 'change-lifecycle' ? 'auto_renew' : 'amount',
            ])
          )
            fail('Unexpected approved change payload');
          if (
            (mode === 'change-conflict' || mode === 'change-source-conflict') &&
            attempts === 1
          ) {
            version = 2;
            return new Response('{}', { status: 409 });
          }
          if (mode === 'change-uncertain' && attempts === 1) {
            firstChangeBody = body;
            return new Response('{}', { status: 503 });
          }
          if (firstChangeBody && !equal(firstChangeBody, body))
            fail('Uncertain retry changed its frozen request');
          const after = {
            ...before,
            ...(mode === 'change-lifecycle'
              ? { auto_renew: false }
              : { amount: '19' }),
            version: version + 1,
          };
          applied = {
            request_id: body.request_id,
            applied_version: after.version,
            created_at: '2026-11-03T00:00:00Z',
            selected_fields: [
              mode === 'change-lifecycle' ? 'auto_renew' : 'amount',
            ],
            before,
            after,
            source,
            additional_evidence: [],
            algorithm_version: 'subscription-change-v1',
          };
          return response({ receipt: applied, current: after });
        }
        if (
          method === 'GET' &&
          url.pathname === path + '/changes' &&
          url.search === '?limit=20&offset=0'
        )
          return response({
            changes: applied ? [applied] : [],
            total: applied ? 1 : 0,
            limit: 20,
            offset: 0,
          });
        fail('Unexpected change route');
      }
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
      if (mode === 'lifecycle-raw-edit') {
        keys(body, [
          'contract_kind',
          'effective_date',
          'access_end_date',
          'trial_end_date',
          'auto_renew',
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
          'expected_version',
          'next_billing_date_explicit',
        ]);
        if (
          method !== 'PUT' ||
          url.pathname !== `/synthetic/v1/subscriptions/${initial.id}` ||
          body.expected_version !== version ||
          !body.next_billing_date_explicit ||
          !body.confirmed ||
          body.status !== 'ended' ||
          body.auto_renew !== false ||
          body.next_billing_date !== (version === 1 ? '2027-11-01' : null)
        )
          fail(
            'Raw manual date must survive unrelated edits and permit explicit clear',
          );
        version++;
        return response({
          ...initial,
          ...body,
          next_billing_date: null,
          manual_next_billing_date: body.next_billing_date,
          version,
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
        'trial_end_date',
        'auto_renew',
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
          ? ['expected_version', 'next_billing_date_explicit']
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
      const expectedMoney =
        mode === 'register' || mode === 'lifecycle-create'
          ? [null, null]
          : ['9', 'USD'];
      if (!equal([body.amount, body.currency], expectedMoney))
        fail('Unexpected money pair');
      if (
        !equal(
          [body.interval_unit, body.interval_count],
          mode === 'register' || editing ? ['year', 1] : [null, null],
        )
      )
        fail('Unexpected optional cycle');
      if (
        mode === 'lifecycle-create' &&
        (body.trial_end_date !== '2028-02-29' ||
          body.auto_renew !== false ||
          body.access_end_date !== '2028-03-01')
      )
        fail('Explicit lifecycle facts were changed');
      return response({ ...initial, ...body, version: editing ? 2 : 1 });
    } catch (error) {
      window.browserTest.errors.push(String(error));
      throw error;
    }
  };
}
