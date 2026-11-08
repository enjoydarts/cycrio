import assert from 'node:assert/strict';
import test from 'node:test';
import { pageForPath, pageEventProperties } from './telemetry-pages.ts';

const sessionID = '11111111-1111-4111-8111-111111111111';
const pageviewID = '22222222-2222-4222-8222-222222222222';
const origin = 'https://cycrio.example';
const page = { name: 'mail', path: '/mail' };

test('known routes produce fixed page names and remove subscription identifiers', () => {
  for (const [path, name] of [
    ['/', 'home'],
    ['/mail', 'mail'],
    ['/import', 'import'],
    ['/subscriptions', 'subscriptions'],
    ['/analysis', 'analysis'],
    ['/reports', 'reports'],
    ['/admin', 'admin'],
    ['/settings', 'settings'],
  ]) {
    assert.deepEqual(pageForPath(path), { name, path });
  }
  for (const id of [
    'abcdef01-abcd-4abc-8abc-abcdef012345',
    'ABCDEF01-ABCD-4ABC-8ABC-ABCDEF012345',
  ]) {
    assert.deepEqual(pageForPath(`/subscriptions/${id}`), {
      name: 'subscription_detail',
      path: '/subscriptions/detail',
    });
  }
  assert.deepEqual(pageForPath('/mail/'), page);
});

test('settings use a closed set of sections and canonicalize AI aliases', () => {
  for (const section of [
    'mail',
    'appearance',
    'account',
    'ai',
    'credentials',
    'integrations',
    'catalog',
    'usage',
  ]) {
    assert.deepEqual(pageForPath(`/settings/${section}`), {
      name: `settings_${section}`,
      path: `/settings/${section}`,
    });
  }
  for (const alias of ['ai-models', 'ai-runtime', 'mail-policy']) {
    assert.deepEqual(pageForPath(`/settings/${alias}`), {
      name: 'settings_ai',
      path: '/settings/ai',
    });
  }
});

test('untrusted paths, callback data and non-route identifiers are discarded', () => {
  for (const path of [
    '',
    '/unknown',
    '/sso-callback',
    '/sso-callback?code=secret',
    '/mail?email=person@example.com',
    '/mail#secret',
    'https://cycrio.example/mail',
    '//cycrio.example/mail',
    '/settings/person@example.com',
    '/settings/__proto__',
    '/subscriptions/person@example.com',
    '/subscriptions/detail',
    `/subscriptions/${sessionID}/edit`,
    `/subscriptions/${sessionID.replaceAll('-', '%2D')}`,
    '/%6dail',
    '/mail//',
    '/settings/../mail',
    '/mail\\',
    '/mail\n',
  ]) {
    assert.equal(pageForPath(path), null, path);
  }
});

test('page properties contain only the safe page and generated identifiers', () => {
  const enriched = {
    ...page,
    email: 'person@example.com',
    title: 'secret',
    input: 'private',
  };
  assert.deepEqual(
    pageEventProperties(enriched, origin, sessionID, pageviewID),
    {
      $current_url: 'https://cycrio.example/mail',
      $host: 'cycrio.example',
      $pathname: '/mail',
      page_name: 'mail',
      $session_id: sessionID,
      $pageview_id: pageviewID,
      $process_person_profile: false,
      $geoip_disable: true,
      runtime: 'browser',
    },
  );
  assert.equal(
    pageEventProperties(page, 'http://localhost:3100', sessionID, pageviewID)
      ?.$host,
    'localhost:3100',
  );
  assert.equal(
    pageEventProperties(
      { name: 'subscription_detail', path: '/subscriptions/detail' },
      origin,
      sessionID,
      pageviewID,
    )?.$pathname,
    '/subscriptions/detail',
  );
});

test('forged names and paths cannot enter events', () => {
  for (const forged of [
    { name: 'person@example.com', path: '/mail' },
    { name: 'mail', path: '/mail?secret=yes' },
    { name: 'mail', path: '/reports' },
    { name: 'mail', path: '/mail/' },
    { name: 'settings_ai', path: '/settings/ai-models' },
    { name: 'subscription_detail', path: `/subscriptions/${sessionID}` },
  ]) {
    assert.equal(
      pageEventProperties(forged, origin, sessionID, pageviewID),
      null,
    );
  }
});

test('origins must be exact HTTP origins without private URL components', () => {
  for (const invalid of [
    '',
    'garbage',
    'ftp://cycrio.example',
    'https://user:secret@cycrio.example',
    'https://cycrio.example/',
    'https://cycrio.example/mail',
    'https://cycrio.example?secret=yes',
    'https://cycrio.example#secret',
    ' https://cycrio.example',
    'https://cycrio.example\n',
  ]) {
    assert.equal(
      pageEventProperties(page, invalid, sessionID, pageviewID),
      null,
      invalid,
    );
  }
});

test('identifiers require UUIDs instead of personal data', () => {
  for (const invalid of [
    '',
    'person@example.com',
    'user_clerk123',
    `${sessionID}\n`,
    `${sessionID}?secret`,
  ]) {
    assert.equal(pageEventProperties(page, origin, invalid, pageviewID), null);
    assert.equal(pageEventProperties(page, origin, sessionID, invalid), null);
  }
});

test('pageleave duration is bounded and refers only to the sanitized page', () => {
  for (const duration of [0, 0.125, 86400]) {
    const properties = pageEventProperties(
      page,
      origin,
      sessionID,
      pageviewID,
      duration,
    );
    assert.equal(properties?.$prev_pageview_duration, duration);
    assert.equal(properties?.$prev_pageview_pathname, '/mail');
    assert.equal(properties?.$prev_pageview_id, pageviewID);
    assert.equal(properties?.$prev_pageview_host, 'cycrio.example');
    assert.equal(Object.keys(properties ?? {}).length, 13);
  }
  for (const duration of [-1, 86400.1, NaN, Infinity]) {
    assert.equal(
      pageEventProperties(page, origin, sessionID, pageviewID, duration),
      null,
    );
  }
});
