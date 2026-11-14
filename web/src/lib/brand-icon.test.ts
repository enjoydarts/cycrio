import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';

test('service avatars use a generic subscription symbol for unknown names without disclosing the name', async () => {
  const { default: ServiceAvatar } = await import('./ServiceAvatar.svelte');
  const html = render(ServiceAvatar, {
    props: { name: '<script>secret</script>' },
  }).body;
  assert.match(html, /<svg/);
  assert.match(html, /aria-hidden="true"/);
  assert.doesNotMatch(html, /secret|<img|<script/);
});

test('known services render a local decorative image with fixed intrinsic dimensions', async () => {
  const { default: BrandIcon } = await import('./BrandIcon.svelte');
  const html = render(BrandIcon, { props: { name: 'Spotify' } }).body;
  assert.match(html, /<img[^>]*src="\/[^" ]+"/);
  assert.match(html, /alt=""/);
  assert.match(html, /width="42"/);
  assert.match(html, /height="42"/);
  assert.match(html, /aria-hidden="true"/);
  assert.doesNotMatch(html, /https?:/);
});

test('The X Subscription renders the X image at list and detail sizes', async () => {
  const { default: BrandIcon } = await import('./BrandIcon.svelte');
  for (const [size, pixels] of [
    ['small', 28],
    ['large', 42],
  ] as const) {
    const html = render(BrandIcon, {
      props: { name: 'The X Subscription', size },
    }).body;
    assert.match(html, /<img[^>]*src="\/service-logos\/x\.svg"/);
    assert.match(html, new RegExp(`width="${pixels}"`));
    assert.doesNotMatch(html, /<svg|https?:/);
  }
});

test('U-NEXT billing names render the local logo at list and detail sizes', async () => {
  const { default: BrandIcon } = await import('./BrandIcon.svelte');
  for (const size of ['small', 'large'] as const) {
    const html = render(BrandIcon, {
      props: { name: 'ユ－ネクストサ－ビス', size },
    }).body;
    assert.match(html, /<img[^>]*src="\/brand\/services\/unext\.ico"/);
    assert.doesNotMatch(html, /<svg|ユ－ネクストサ－ビス/);
  }
});

test('mail providers take priority over service names and support compact sizes', async () => {
  const { default: BrandIcon } = await import('./BrandIcon.svelte');
  const html = render(BrandIcon, {
    props: { name: 'Spotify', provider: 'gmail', size: 'small' },
  }).body;
  assert.match(html, /<img/);
  assert.doesNotMatch(html, /spotify/);
  assert.match(html, /width="28"/);
  const medium = render(BrandIcon, {
    props: { provider: 'unknown', size: 'medium' },
  }).body;
  assert.match(medium, /<svg/);
  assert.match(medium, /width="32"/);
  assert.doesNotMatch(medium, /<img/);
});

test('light-colored brands select a dark plate without changing the logo color', async () => {
  const { default: BrandIcon } = await import('./BrandIcon.svelte');
  const html = render(BrandIcon, { props: { name: 'Snapchat' } }).body;
  assert.match(html, /<img/);
  assert.match(html, /class="[^"]*\bbranded\b[^"]*\bdark\b/);
  const ordinary = render(BrandIcon, { props: { name: 'Spotify' } }).body;
  assert.doesNotMatch(ordinary, /class="[^"]*\bdark\b/);
});

test('IMAP account host selects the local service image without rendering connection metadata', async () => {
  const { default: BrandIcon } = await import('./BrandIcon.svelte');
  const html = render(BrandIcon, {
    props: {
      provider: 'imap',
      host: 'imap.mail.yahoo.co.jp',
      name: 'personal-label',
    },
  }).body;
  assert.match(html, /<img/);
  assert.match(html, /yahoojp/);
  assert.doesNotMatch(html, /imap\.mail|personal-label|https?:/);
});
