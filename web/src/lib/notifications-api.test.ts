import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createNotificationsAPI,
  parseNotificationSettings,
  parseNotificationPage,
  parseNotification,
} from './notifications-api.ts';
import {
  notificationSettings,
  notificationPage,
  notification,
  notificationID,
} from './notifications-fixtures.ts';
test('notification settings validate all five kinds, three readiness states and explicit safe values', () => {
  assert.equal(
    parseNotificationSettings(notificationSettings()).preferences.email_opt_in,
    false,
  );
  for (const patch of [
    { lead_days: 31 },
    { send_hour: -1 },
    { timezone: 'UTC' },
    { email_opt_in: 'true' },
    { kinds: {} },
  ])
    assert.throws(() =>
      parseNotificationSettings({
        ...notificationSettings(),
        preferences: { ...notificationSettings().preferences, ...patch },
      }),
    );
  assert.throws(() =>
    parseNotificationSettings({
      ...notificationSettings(),
      preferences: {
        ...notificationSettings().preferences,
        kinds: {
          ...notificationSettings().preferences.kinds,
          renewal: { in_app: true, email: true },
        },
      },
    }),
  );
});
test('notification pages preserve native price facts and source identity while rejecting malformed transport', () => {
  assert.equal(
    parseNotificationPage(notificationPage()).notifications[0].facts.new_amount,
    '19',
  );
  assert.equal(parseNotification(notification()).facts.event_index, 0);
  for (const patch of [
    { kind: 'unknown' },
    { event_index: 32 },
    { new_amount: 19 },
    { result_id: 'not-an-id' },
    { in_app: 'true' },
  ])
    assert.throws(() =>
      parseNotification({
        ...notification(),
        facts: { ...notification().facts, ...patch },
      }),
    );
  assert.throws(() =>
    parseNotificationPage({ ...notificationPage(), unread_count: -1 }),
  );
});
test('notification API sends only known preferences, read confirmation and owner-neutral paging', async () => {
  const calls: any[] = [];
  const api = createNotificationsAPI('/api', async (url, options) => {
    calls.push({
      url: String(url),
      method: options?.method,
      body: options?.body ? JSON.parse(String(options.body)) : null,
    });
    return Response.json(
      String(url).includes('notification-settings')
        ? {
            ...notificationSettings(),
            preferences: { ...notificationSettings().preferences, version: 2 },
          }
        : String(url).endsWith('/read')
          ? { ...notification(), read_at: '2026-11-03T00:01:00Z' }
          : notificationPage(),
    );
  });
  await api.list({ read: 'unread', limit: 20, offset: 20 }, async () => 't');
  await api.read(notificationID, async () => 't');
  const p = notificationSettings().preferences;
  await api.updateSettings(
    {
      ...p,
      expected_version: 1,
      confirmed: true,
      recipient: 'forbidden@example.test',
    } as never,
    async () => 't',
  );
  assert.equal(
    calls[0].url,
    '/api/v1/notifications?limit=20&offset=20&read=unread',
  );
  assert.deepEqual(calls[1].body, { confirmed: true });
  assert.deepEqual(Object.keys(calls[2].body).sort(), [
    'confirmed',
    'email_opt_in',
    'expected_version',
    'kinds',
    'lead_days',
    'send_hour',
    'timezone',
  ]);
  await assert.rejects(
    api.list({ read: 'all', limit: 20, offset: -1 }, async () => 't'),
  );
  assert.equal(calls.length, 3);
});
