import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { createNotificationInbox } from './notification-inbox-controller.ts';
import {
  notificationPage,
  notification,
  notificationID,
} from './notifications-fixtures.ts';
const filter = { read: 'all' as const, limit: 20, offset: 0 };
test('notification reads are explicit, owner-page bound and refresh unread counts after acknowledgement', async () => {
  let reads = 0,
    lists = 0,
    unread = -1;
  const c = createNotificationInbox(
    {
      list: async () => {
        lists++;
        return {
          ...notificationPage(),
          unread_count: reads ? 0 : 1,
          notifications: [
            {
              ...notification(),
              read_at: reads ? '2026-11-03T00:01:00Z' : null,
            },
          ],
        };
      },
      read: async () => {
        reads++;
        return { ...notification(), read_at: '2026-11-03T00:01:00Z' };
      },
    },
    async () => 't',
    () => {},
    () => {},
    (value) => (unread = value),
  );
  await c.load(filter);
  assert.equal(reads, 0);
  assert.equal(unread, 1);
  await c.read('11111111-1111-4111-8111-111111111111');
  assert.equal(reads, 0);
  await c.read(notificationID);
  assert.equal(reads, 1);
  assert.equal(lists, 2);
  assert.equal(unread, 0);
  await c.read(notificationID);
  assert.equal(reads, 1);
});
test('notification owner denial purges facts and stale list requests cannot replace a newer page', async () => {
  let resolve!: (value: ReturnType<typeof notificationPage>) => void,
    denied = 0;
  const c = createNotificationInbox(
    {
      list: (f) =>
        f.offset
          ? Promise.resolve({ ...notificationPage(), offset: 20 })
          : new Promise((r) => (resolve = r)),
      read: async () => {
        throw new ApiError('access_denied');
      },
    },
    async () => 't',
    () => {},
    () => denied++,
    () => {},
  );
  const old = c.load(filter);
  await c.load({ ...filter, offset: 20 });
  resolve(notificationPage());
  await old;
  assert.equal(c.state().page?.offset, 20);
  await c.read(notificationID);
  assert.equal(c.state().page, null);
  assert.equal(denied, 1);
});
test('disposed inbox never publishes late previous-owner notification facts', async () => {
  let resolve!: (value: ReturnType<typeof notificationPage>) => void,
    publications = 0,
    unread = -1;
  const c = createNotificationInbox(
    {
      list: () => new Promise((r) => (resolve = r)),
      read: async () => notification(),
    },
    async () => 'old-owner',
    () => publications++,
    () => {},
    (value) => (unread = value),
  );
  const pending = c.load(filter);
  c.dispose();
  const before = publications;
  resolve(notificationPage());
  await pending;
  assert.equal(publications, before);
  assert.equal(c.state().page, null);
  assert.equal(unread, -1);
});
