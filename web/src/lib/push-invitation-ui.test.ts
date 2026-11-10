import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';
import { createRawSnippet } from 'svelte';
const Modal = (await import('./Modal.svelte')).default;
const Invitation = (await import('./PushInvitation.svelte')).default;
test('通知の案内は共通Modalのコンパクト表示を使用する', () => {
  const html = render(Modal, {
    props: {
      open: false,
      size: 'compact',
      title: '通知',
      onclose() {},
      children: createRawSnippet(() => ({ render: () => '<p>案内</p>' })),
    },
  }).body;
  assert.match(html, /<dialog[^>]*class="[^"]*compact/);
});
test('標準の有効にする・あとでポップアップをSDKなしで描画できる', () => {
  const html = render(Invitation, {
    props: {
      owner: 'user',
      pathname: '/',
      token: async () => null,
      pushController: {} as any,
      notificationsAPI: {} as any,
      browser: {} as any,
      onDenied() {},
    },
  }).body;
  assert.match(html, /Push通知を有効にしますか？/);
  assert.match(html, />有効にする<|>\s*有効にする\s*</);
  assert.match(html, />あとで<|>\s*あとで\s*</);
  assert.doesNotMatch(html, /<script[^>]+onesignal/);
});
