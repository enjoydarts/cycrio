import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { createNotificationSettings } from './notification-settings-controller.ts';
import { notificationSettings } from './notifications-fixtures.ts';
test('email consent can be withdrawn with all email kinds after sender readiness is lost', async () => {
  const page = notificationSettings();
  page.email.reason = 'sender_unavailable';
  page.preferences.email_opt_in = true;
  page.preferences.kinds.renewal.email = true;
  let writes = 0;
  const c = createNotificationSettings(
    {
      settings: async () => page,
      updateSettings: async (input) => {
        writes++;
        assert.equal(input.email_opt_in, false);
        assert.ok(Object.values(input.kinds).every((kind) => !kind.email));
        return { ...page, preferences: { ...input, version: 2 } };
      },
    },
    async () => 't',
    () => {},
    () => {},
  );
  await c.load();
  c.edit({ email_opt_in: false });
  c.confirm(true);
  await c.save();
  assert.equal(writes, 1);
  assert.equal(c.state().saved, true);
  assert.equal(c.state().values?.kinds.renewal.in_app, true);
});
test('notification settings require confirmation, preserve known failures and never save unprepared email opt-in', async () => {
  let writes = 0;
  const c = createNotificationSettings(
    {
      settings: async () => notificationSettings(),
      updateSettings: async () => {
        writes++;
        return {
          ...notificationSettings(),
          preferences: {
            ...notificationSettings().preferences,
            version: 2,
            lead_days: 3,
          },
        };
      },
    },
    async () => 't',
    () => {},
    () => {},
  );
  await c.load();
  c.edit({ lead_days: 3 });
  await c.save();
  assert.equal(writes, 0);
  c.confirm(true);
  await c.save();
  assert.equal(writes, 1);
  assert.equal(c.state().saved, true);
  c.edit({ email_opt_in: true });
  c.confirm(true);
  await c.save();
  assert.equal(writes, 1);
});
test('lost settings response uses GET and exact CAS snapshot comparison, never repeats the mutation', async () => {
  let writes = 0,
    page = notificationSettings();
  const c = createNotificationSettings(
    {
      settings: async () => structuredClone(page),
      updateSettings: async () => {
        writes++;
        throw new ApiError('dependency_unavailable');
      },
    },
    async () => 't',
    () => {},
    () => {},
  );
  await c.load();
  c.edit({ lead_days: 0 });
  c.confirm(true);
  await c.save();
  assert.equal(c.state().uncertain, true);
  c.edit({ lead_days: 9 });
  assert.equal(c.state().values?.lead_days, 0);
  await c.reconcile();
  assert.equal(c.state().uncertain, true);
  assert.equal(writes, 1);
  page = {
    ...page,
    preferences: { ...page.preferences, version: 2, lead_days: 0 },
  };
  await c.reconcile();
  assert.equal(c.state().saved, true);
  assert.equal(c.state().uncertain, false);
  assert.equal(writes, 1);
});
test('settings conflict and permission loss cannot silently overwrite newer owner preferences', async () => {
  let denied = 0,
    page = notificationSettings();
  const c = createNotificationSettings(
    {
      settings: async () => page,
      updateSettings: async () => {
        throw new ApiError('dependency_unavailable');
      },
    },
    async () => 't',
    () => {},
    () => denied++,
  );
  await c.load();
  c.edit({ lead_days: 3 });
  c.confirm(true);
  await c.save();
  page = {
    ...page,
    preferences: { ...page.preferences, version: 3, lead_days: 10 },
  };
  await c.reconcile();
  assert.equal(c.state().conflict, true);
  assert.equal(c.state().saved, false);
  assert.equal(c.state().values?.lead_days, 3);
  const d = createNotificationSettings(
    {
      settings: async () => {
        throw new ApiError('access_denied');
      },
      updateSettings: async () => page,
    },
    async () => 't',
    () => {},
    () => denied++,
  );
  await d.load();
  assert.equal(d.state().values, null);
  assert.equal(denied, 1);
});
test('disposed settings never publish a late previous-owner response', async () => {
  let resolve!: (value: ReturnType<typeof notificationSettings>) => void,
    publications = 0;
  const c = createNotificationSettings(
    {
      settings: () => new Promise((r) => (resolve = r)),
      updateSettings: async () => notificationSettings(),
    },
    async () => 'old-owner',
    () => publications++,
    () => {},
  );
  const pending = c.load();
  c.dispose();
  const before = publications;
  resolve(notificationSettings());
  await pending;
  assert.equal(publications, before);
  assert.equal(c.state().values, null);
});
test('reactive preference objects can update one channel without losing the other choices', async () => {
  const c = createNotificationSettings(
    {
      settings: async () => notificationSettings(),
      updateSettings: async () => notificationSettings(),
    },
    async () => 't',
    () => {},
    () => {},
  );
  await c.load();
  const kinds = notificationSettings().preferences.kinds;
  c.edit({
    kinds: {
      ...kinds,
      renewal: { in_app: false, email: false },
      trial_end: new Proxy(kinds.trial_end, {}),
    },
  });
  assert.equal(c.state().values?.kinds.renewal.in_app, false);
  assert.equal(c.state().values?.kinds.trial_end.in_app, true);
});
