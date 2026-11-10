import {
  notification,
  notificationSettings,
} from '../src/lib/notifications-fixtures.ts';
import { changeEvidence } from '../src/lib/subscription-changes-fixtures.ts';
import type {
  Notification,
  NotificationPreferences,
} from '../src/lib/notifications-api.ts';
const response = (value: unknown, status = 200) =>
  Response.json(value, { status });
export function notificationFixture(mode: string) {
  let preferences = notificationSettings().preferences,
    writes = 0;
  const rows: Notification[] = [
    notification(),
    {
      ...notification(),
      id: '88888888-8888-4888-8888-888888888888',
      facts: {
        kind: 'trial_end',
        source_key: 'synthetic-trial',
        contract_id: notification().facts.contract_id,
        service: 'Synthetic Trial',
        due_date: '2026-11-10',
        scheduled_at: '2026-11-03T00:00:00Z',
        date_source: 'explicit_trial_end',
        in_app: true,
        email: false,
        push: false,
      },
    },
  ];
  const candidate = {
    result_id: notification().facts.result_id!,
    event_index: 0,
    account_id: '11111111-1111-4111-8111-111111111111',
    account_label: '合成接続',
    received_at: '2026-11-03T00:00:00Z',
    event: changeEvidence().original_event,
    mail_count: 1,
    related_count: 1,
  };
  return (
    url: URL,
    method: string,
    body: Record<string, unknown> | null,
  ): Response | Promise<Response> => {
    if (url.pathname === '/synthetic/v1/notifications' && method === 'GET') {
      if (mode === 'notifications-owner-change') {
        const page = {
          notifications: [],
          total: 0,
          unread_count: 0,
          limit: Number(url.searchParams.get('limit')),
          offset: 0,
          as_of: '2026-11-03',
          timezone: 'Asia/Tokyo',
        };
        if (window.browserTest.ownerGeneration === 2) return response(page);
        return new Promise((resolve) => {
          window.browserTest.releaseOldNotification = () =>
            resolve(
              response({
                ...page,
                notifications: [
                  {
                    ...rows[0],
                    facts: { ...rows[0].facts, service: 'Old owner synthetic' },
                  },
                ],
                total: 9,
                unread_count: 9,
              }),
            );
        });
      }
      const p = Object.fromEntries(url.searchParams);
      if (
        Object.keys(p).sort().join() !== 'limit,offset,read' ||
        !['1', '20'].includes(p.limit) ||
        !['0', '20'].includes(p.offset) ||
        !['all', 'unread', 'read'].includes(p.read)
      )
        throw Error('Unexpected notification paging');
      const chosen = rows.filter(
        (n) =>
          p.read === 'all' ||
          (p.read === 'unread' ? n.read_at === null : n.read_at !== null),
      );
      return response({
        notifications:
          p.offset === '20'
            ? chosen.slice(-1)
            : chosen.slice(0, Number(p.limit)),
        total: p.read === 'all' ? 24 : chosen.length,
        unread_count: rows.filter((n) => n.read_at === null).length,
        limit: Number(p.limit),
        offset: Number(p.offset),
        as_of: '2026-11-03',
        timezone: 'Asia/Tokyo',
      });
    }
    const read = url.pathname.match(
      /^\/synthetic\/v1\/notifications\/([a-f0-9-]+)\/read$/,
    );
    if (read && method === 'POST') {
      if (JSON.stringify(body) !== '{"confirmed":true}')
        throw Error('Unexpected read payload');
      const row = rows.find((n) => n.id === read[1]);
      if (!row) throw Error('Unknown read identity');
      row.read_at ??= '2026-11-03T00:01:00Z';
      return response(row);
    }
    if (url.pathname === '/synthetic/v1/notification-settings') {
      if (method === 'PUT') {
        writes++;
        if (
          writes !== 1 ||
          !body ||
          Object.keys(body).sort().join() !==
            'confirmed,email_opt_in,expected_version,kinds,lead_days,push_opt_in,send_hour,timezone' ||
          body.expected_version !== 1 ||
          body.confirmed !== true ||
          body.lead_days !== 3 ||
          body.send_hour !== 21 ||
          body.email_opt_in !== (mode === 'notifications') ||
          body.timezone !== 'Asia/Tokyo'
        )
          throw Error('Unexpected notification preferences');
        const kinds = body.kinds as NotificationPreferences['kinds'];
        if (
          kinds.renewal.in_app !== false ||
          kinds.renewal.email !== (mode === 'notifications') ||
          Object.entries(kinds).some(
            ([kind, c]) => kind !== 'renewal' && c.email,
          ) ||
          Object.entries(kinds).some(
            ([kind, c]) => kind !== 'renewal' && !c.in_app,
          )
        )
          throw Error('Unexpected notification channels');
        preferences = {
          ...preferences,
          version: 2,
          kinds,
          lead_days: 3,
          send_hour: 21,
          email_opt_in: mode === 'notifications',
        };
        if (mode === 'notifications-uncertain') return response({}, 503);
      } else if (method !== 'GET') throw Error('Unexpected settings method');
      return response({
        ...notificationSettings(),
        preferences,
        ...(mode === 'notifications'
          ? {
              email: {
                available: true,
                reason: '',
                profile_ready: true,
                sender_ready: true,
              },
            }
          : {}),
      });
    }
    if (
      url.pathname.endsWith('/deliveries') &&
      url.pathname.startsWith('/synthetic/v1/notifications/') &&
      method === 'GET'
    )
      return response({
        deliveries: [
          {
            id: '99999999-9999-4999-8999-999999999999',
            channel: 'in_app',
            state: 'accepted',
            reason: '',
            scheduled_at: '2026-11-03T00:00:00Z',
            created_at: '2026-11-03T00:00:00Z',
            updated_at: '2026-11-03T00:00:00Z',
            sent: false,
            delivered: false,
            bounced: false,
            complained: false,
            failed: false,
          },
        ],
      });
    if (
      url.pathname ===
        `/synthetic/v1/subscription-candidates/${candidate.result_id}/0` &&
      method === 'GET'
    )
      return mode === 'notifications-stale'
        ? response({}, 404)
        : response(candidate);
    if (
      url.pathname === '/synthetic/v1/subscriptions/candidates' &&
      method === 'GET'
    )
      return response({
        candidates: mode === 'notifications-stale' ? [] : [candidate],
        total: mode === 'notifications-stale' ? 0 : 1,
        limit: 20,
        offset: 0,
      });
    throw Error(
      'Unexpected notification fixture route ' + method + ' ' + url.pathname,
    );
  };
}
