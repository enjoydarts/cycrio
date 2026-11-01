import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';

test('popular services and Japanese names resolve to the appropriate product rather than the parent brand', async () => {
  const { resolveServiceLogo } = await import('./service-logos.ts');
  for (const [name, slug] of [
    ['Netflix', 'netflix'],
    ['ネットフリックス', 'netflix'],
    ['Spotify Premium', 'spotify'],
    ['Ｓｐｏｔｉｆｙ', 'spotify'],
    ['YouTube Premium', 'youtube'],
    ['ユーチューブ プレミアム', 'youtube'],
    ['ChatGPT Plus', 'openai'],
    ['チャットGPT', 'openai'],
    ['Google One 2TB', 'googleone'],
    ['Google Drive', 'googledrive'],
    ['Google Workspace', 'googleworkspace'],
    ['Apple Music', 'applemusic'],
    ['iCloud+ 200GB', 'icloud'],
    ['Amazon Prime', 'amazonprime'],
    ['AWS', 'amazonwebservices'],
    ['Microsoft 365 Personal', 'microsoft365'],
    ['OneDrive', 'onedrive'],
    ['GitHub Copilot', 'githubcopilot'],
    ['Adobe Creative Cloud', 'adobecreativecloud'],
    ['Disney+', 'disneyplus'],
    ['Notion Plus', 'notion'],
    ['Dropbox', 'dropbox'],
    ['Amazon', 'amazon'],
    ['Adobe', 'adobe'],
    ['Slack Pro', 'slack'],
    ['Nintendo Switch Online', 'nintendo'],
    ['Xbox Game Pass', 'xbox'],
    ['Hulu', 'hulu'],
    ['U-NEXT', 'unext'],
    ['dアニメストア', 'danimestore'],
    ['Prime Video', 'amazonprimevideo'],
  ]) {
    const logo = resolveServiceLogo(name);
    assert.ok(logo, name);
    assert.equal(logo.slug, slug, name);
    assert.match(logo.src, /^\/(?:service-logos|brand\/services)\//);
  }
});

test('catalog lookup covers ordinary brands without a per-service implementation', async () => {
  const { resolveServiceLogo } = await import('./service-logos.ts');
  assert.equal(resolveServiceLogo('Linear')?.slug, 'linear');
  assert.equal(resolveServiceLogo('JetBrains')?.slug, 'jetbrains');
  assert.equal(resolveServiceLogo('Proton Mail')?.slug, 'protonmail');
});

test('mail providers are independent from personal connection labels', async () => {
  const { resolveMailLogo } = await import('./service-logos.ts');
  for (const [name, slug] of [
    ['gmail', 'gmail'],
    ['google_workspace', 'gmail'],
    ['outlook', 'outlook'],
    ['hotmail', 'outlook'],
    ['fastmail', 'fastmail'],
  ])
    assert.equal(resolveMailLogo(name)?.slug, slug);
  assert.equal(resolveMailLogo('imap'), null);
});

test('known domains and URLs map deliberately without trusting misleading hosts or partial names', async () => {
  const { resolveServiceLogo } = await import('./service-logos.ts');
  assert.equal(
    resolveServiceLogo('https://www.netflix.com/account')?.slug,
    'netflix',
  );
  assert.equal(resolveServiceLogo('openai.com')?.slug, 'openai');
  for (const name of [
    'not-netflix.example',
    'https://netflix.com.evil.example',
    'https://evil.example/netflix',
    'https://netflix.com@evil.example',
    'https://evil@netflix.com',
    'javascript:alert(1)',
    '__proto__',
    'Netflix Consulting Ltd',
    'Unknown Subscription',
    '',
    null,
    undefined,
  ])
    assert.equal(resolveServiceLogo(name), null, String(name));
});

test('official brand names that look like domains remain discoverable without accepting lookalikes', async () => {
  const { resolveServiceLogo } = await import('./service-logos.ts');
  for (const name of [
    'Chess.com',
    'Cal.com',
    'bunny.net',
    'Fly.io',
    'Last.fm',
    'mail.com',
    'Z.ai',
  ]) {
    const logo = resolveServiceLogo(name);
    assert.ok(logo, name);
    assert.equal(logo.title.toLowerCase(), name.toLowerCase());
  }
  assert.equal(resolveServiceLogo('https://last.fm/account')?.title, 'Last.fm');
  for (const name of [
    'https://last.fm.evil.example',
    'https://evil.example/last.fm',
    'last.fm.evil.example',
    'net.flix',
  ])
    assert.equal(resolveServiceLogo(name), null);
});

test('ambiguous catalog names never silently choose a different brand', async () => {
  const { resolveServiceLogo } = await import('./service-logos.ts');
  for (const name of [
    'C',
    'C++',
    'C#',
    'Backstage',
    'Graphite',
    'Spring',
    'Hive',
  ])
    assert.equal(resolveServiceLogo(name), null, name);
  assert.equal(resolveServiceLogo('cplusplus')?.slug, 'cplusplus');
});
