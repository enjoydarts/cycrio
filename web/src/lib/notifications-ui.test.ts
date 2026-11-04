import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';
import { createRawSnippet } from 'svelte';
import { readFile } from 'node:fs/promises';
import { parse } from 'svelte/compiler';
import {
  notification,
  notificationSettings,
} from './notifications-fixtures.ts';
import { createNotificationsAPI } from './notifications-api.ts';
import { deliveryState } from './notification-text.ts';
const List = (await import('./NotificationList.svelte')).default;
const Fields = (await import('./NotificationPreferenceFields.svelte')).default;
const Shell = (await import('./AppShell.svelte')).default;
test('unavailable email sender explains the reason and leaves existing consent revocable', () => {
  const p = notificationSettings();
  p.email.reason = 'sender_unavailable';
  p.preferences.email_opt_in = true;
  p.preferences.kinds.renewal.email = true;
  const html = render(Fields, {
    props: { values: p.preferences, email: p.email, onchange: () => {} },
  }).body;
  const consent = html.match(
    /<input[^>]*aria-label="本人の検証済みメールへ通知を送る"[^>]*>/,
  )?.[0];
  assert.ok(consent);
  assert.doesNotMatch(consent, /disabled/);
  assert.match(html, /送信サービスの準備ができていません/);
});
test('compiled notification list calls a price change tentative and links the exact pending source without auto selection', () => {
  const html = render(List, {
    props: {
      notifications: [notification()],
      onRead: () => {},
      onDelivery: () => {},
    },
  }).body;
  assert.match(html, /値上げ候補/);
  assert.match(html, /契約の一致は未確定/);
  assert.match(html, /9 USD/);
  assert.match(html, /19 USD/);
  assert.match(
    html,
    /href="\/analysis\?tab=attention&amp;result_id=66666666-6666-4666-8666-666666666666&amp;event_index=0"/,
  );
  assert.match(html, /既読にする/);
  assert.doesNotMatch(html, /自動で反映|自動で紐付け|synthetic-price-change/);
});
test('compiled preferences expose five in-app options but leave unprepared email controls disabled', () => {
  const p = notificationSettings();
  const html = render(Fields, {
    props: { values: p.preferences, email: p.email, onchange: () => {} },
  }).body;
  assert.equal((html.match(/をアプリ内で通知/g) ?? []).length, 5);
  const email = [
    ...html.matchAll(/<input[^>]*aria-label="[^"]*をメールで通知"[^>]*>/g),
  ].map((x) => x[0]);
  assert.equal(email.length, 5);
  for (const input of email) assert.match(input, /disabled/);
  assert.match(html, /メール通知は準備中/);
  assert.doesNotMatch(html, /type="email"/);
});
test('notification shell wiring enables the real feature and accepted delivery never means delivered', async () => {
  const props = {
    user: { id: notification().facts.contract_id!, role: 'user' as const },
    pathname: '/subscriptions',
    signOut: () => {},
    children: createRawSnippet(() => ({ render: () => '<p>本文</p>' })),
    token: async () => 'synthetic',
    notificationsAPI: createNotificationsAPI('', async () => {
      throw Error('SSR must not fetch');
    }),
  };
  const html = render(Shell, { props }).body;
  const bell =
    html.match(/<button[^>]*aria-label="通知を開く"[^>]*>/)?.[0] ?? '';
  assert.ok(bell);
  assert.doesNotMatch(bell, /disabled/);
  assert.match(html, /<option value="unread" selected/);
  const source = await readFile(
    new URL('../routes/+layout.svelte', import.meta.url),
    'utf8',
  );
  const ast = parse(source, { modern: true }) as any;
  assert.ok(
    ast.instance.content.body.some(
      (n: any) =>
        n.type === 'ImportDeclaration' &&
        n.source.value === '#lib/notifications-api.ts',
    ),
  );
  assert.match(source, /<AppShell[\s\S]*?\{notificationsAPI\}/);
  const delivery = {
    id: notification().id,
    channel: 'email' as const,
    state: 'accepted' as const,
    reason: '',
    scheduled_at: notification().created_at,
    created_at: notification().created_at,
    updated_at: notification().created_at,
    sent: true,
    delivered: false,
    bounced: false,
    complained: false,
    failed: false,
  };
  assert.equal(deliveryState(delivery), '送信受付済み（配信未確認）');
  assert.equal(deliveryState({ ...delivery, delivered: true }), '配信済み');
});
