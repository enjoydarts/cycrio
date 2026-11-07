import assert from 'node:assert/strict';
import test from 'node:test';

test('FranklyMail uses an explicit JMAP preset without migrating IMAP accounts', async () => {
  const { mailServicePresets, mailServiceForAccount } =
    await import('./mail-service-presets.ts');
  const preset = mailServicePresets.find((p) => p.id === 'franklymail');
  assert.ok(preset);
  assert.equal(preset.provider, 'franklymail');
  assert.equal(preset.logoSlug, 'franklymail');
  assert.equal(preset.label, 'FranklyMail（JMAP）');
  assert.equal(preset.host, undefined);
  assert.equal(preset.helpUrl, 'https://franklymail.com/guides/jmap');
  assert.match(preset.setup, /https:\/\/jmap.franklymail.com\/jmap\/session/);
  assert.match(preset.setup, /ダッシュボード|APIキー/);
  assert.equal(
    mailServiceForAccount({ provider: 'franklymail' }).id,
    'franklymail',
  );
  assert.equal(
    mailServiceForAccount({ provider: 'imap', host: 'imap.franklymail.com' })
      .id,
    'imap',
  );
});

for (const [id, host, condition] of [
  ['ocn', 'imap.ocn.ne.jp', /メールパスワード/],
  ['biglobe', 'mail.biglobe.ne.jp', /スタンダード/],
  ['sonet', 'imap.so-net.ne.jp', /有料.*IMAP|IMAP.*有料/],
  ['nifty', 'imap.nifty.com', /メールソフト接続許可/],
] as const) {
  test(`domestic mail preset ${id} preserves official endpoint, requirements and strict account recognition`, async () => {
    const { mailServicePresets, mailServiceForAccount } =
      await import('./mail-service-presets.ts');
    const { resolveMailLogo } = await import('./service-logos.ts');
    const preset = mailServicePresets.find((p) => p.id === id);
    assert.ok(preset, id);
    assert.equal(preset.provider, 'imap');
    assert.equal(preset.host, host);
    assert.equal(preset.port, 993);
    assert.match(preset.setup, condition);
    assert.match(preset.helpUrl!, /^https:\/\//);
    assert.equal(mailServiceForAccount({ provider: 'imap', host }).id, id);
    assert.equal(resolveMailLogo('imap', host)?.slug, id);
    assert.match(
      resolveMailLogo('imap', host)!.src,
      new RegExp(`^/brand/services/${id}\\.`),
    );
    for (const lookalike of [
      `${host}.example.com`,
      `fake-${host}`,
      `https://${host}`,
      `${host}:993`,
    ]) {
      assert.equal(
        mailServiceForAccount({ provider: 'imap', host: lookalike }).id,
        'imap',
      );
      assert.equal(resolveMailLogo('imap', lookalike), null);
    }
  });
}

test('major IMAP presets have separate Yahoo Japan and global endpoints with official setup instructions', async () => {
  const { mailServicePresets } = await import('./mail-service-presets.ts');
  for (const [id, host] of [
    ['yahoojp', 'imap.mail.yahoo.co.jp'],
    ['yahoo', 'imap.mail.yahoo.com'],
    ['icloudmail', 'imap.mail.me.com'],
    ['aolmail', 'imap.aol.com'],
    ['zohomail', 'imap.zoho.com'],
    ['gmx', 'imap.gmx.com'],
    ['mailcom', 'imap.mail.com'],
  ]) {
    const preset = mailServicePresets.find((p) => p.id === id);
    assert.ok(preset, id);
    assert.equal(preset.provider, 'imap');
    assert.equal(preset.host, host);
    assert.equal(preset.port, 993);
    assert.match(preset.helpUrl!, /^https:\/\//);
    assert.ok(preset.setup.length > 10);
  }
  assert.match(
    mailServicePresets.find((p) => p.id === 'yahoojp')!.setup,
    /外部|公式サービス以外/,
  );
  assert.match(
    mailServicePresets.find((p) => p.id === 'icloudmail')!.setup,
    /アプリ用パスワード/,
  );
  assert.match(
    mailServicePresets.find((p) => p.id === 'zohomail')!.setup,
    /地域|データセンター/,
  );
  assert.match(
    mailServicePresets.find((p) => p.id === 'mailcom')!.setup,
    /Premium/,
  );
});

test('known IMAP hosts identify existing accounts without trusting personal labels or similar hosts', async () => {
  const { mailServiceForAccount } = await import('./mail-service-presets.ts');
  assert.equal(
    mailServiceForAccount({ provider: 'imap', host: 'imap.mail.yahoo.co.jp' })
      .id,
    'yahoojp',
  );
  assert.equal(
    mailServiceForAccount({ provider: 'imap', host: 'IMAP.MAIL.YAHOO.COM' }).id,
    'yahoo',
  );
  assert.equal(
    mailServiceForAccount({ provider: 'gmail', host: 'imap.mail.yahoo.co.jp' })
      .id,
    'gmail',
  );
  assert.equal(mailServiceForAccount({ provider: 'fastmail' }).id, 'fastmail');
  for (const host of [
    undefined,
    '',
    'imap.mail.yahoo.co.jp.example.com',
    'fake-imap.mail.yahoo.co.jp',
    'https://imap.mail.yahoo.co.jp',
    'imap.mail.yahoo.co.jp:993',
    '__proto__',
    'localhost',
  ])
    assert.equal(
      mailServiceForAccount({ provider: 'imap', host }).id,
      'imap',
      String(host),
    );
});
