import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';

const domesticServices = [
  [
    'address',
    [
      'ADDress',
      'ＡＤＤｒｅｓｓ',
      'アドレス',
      'ADDress 月額プラン',
      'https://address.love/',
      'address.love',
    ],
  ],
  [
    'bookwalker',
    [
      'BOOK☆WALKER',
      'BOOK WALKER',
      'ＢＯＯＫ☆ＷＡＬＫＥＲ',
      'ブックウォーカー',
      'ブック☆ウォーカー',
      'BOOK☆WALKER 読み放題',
      'BOOK WALKER 読み放題 MAXコース',
      'BOOK☆WALKER 読み放題 マンガコース',
      'BOOK☆WALKER マンガ・雑誌 読み放題',
      'BOOK☆WALKER 文庫・ラノベ 読み放題',
      'https://bookwalker.jp/subscription/',
      'https://global.bookwalker.jp/',
      'bookwalker.jp',
    ],
  ],
  [
    'kakuyomu',
    [
      'カクヨム',
      'Kakuyomu',
      'カクヨムネクスト',
      'カクヨムサポーターズパスポート',
      'カクヨム サポーターズパスポート ライト',
      'カクヨム サポーターズパスポート スタンダード',
      'カクヨム サポーターズパスポート デラックス',
      'https://kakuyomu.jp/next',
      'kakuyomu.jp',
    ],
  ],
  [
    'kadocomi',
    [
      'カドコミ',
      'Kadocomi',
      'ComicWalker',
      'コミックウォーカー',
      'カドコミ（コミックウォーカー）',
      'comic-walker.com',
    ],
  ],
  [
    'kadokawa',
    ['KADOKAWA', '角川', 'カドカワ', '株式会社KADOKAWA', 'kadokawa.co.jp'],
  ],
  [
    'rakutenmagazine',
    [
      '楽天マガジン',
      'Rakuten Magazine',
      '楽天マガジン 年額プラン',
      'magazine.rakuten.co.jp',
    ],
  ],
  [
    'rakutenmusic',
    [
      '楽天ミュージック',
      'Rakuten Music',
      '楽天ミュージック 学生プラン',
      'music.rakuten.co.jp',
    ],
  ],
  [
    'linemusic',
    [
      'LINE MUSIC',
      'ラインミュージック',
      'LINE MUSIC ファミリープラン',
      'music.line.me',
    ],
  ],
  ['dmmtv', ['DMM TV', 'ＤＭＭ ＴＶ', 'DMMプレミアム', 'tv.dmm.com']],
  ['lemino', ['Lemino', 'レミノ', 'Lemino Premium', 'lemino.docomo.ne.jp']],
  ['telasa', ['TELASA', 'テラサ', 'TELASA 月額プラン', 'telasa.jp']],
  [
    'fod',
    ['FOD', 'FODプレミアム', 'フジテレビオンデマンド', 'fod.fujitv.co.jp'],
  ],
  ['wowow', ['WOWOW', 'WOWOWオンデマンド', 'ワウワウ', 'wowow.co.jp']],
  ['radiko', ['radiko', 'ラジコ', 'radikoプレミアム', 'radiko.jp']],
  [
    'nhkondemand',
    [
      'NHKオンデマンド',
      'NHK On Demand',
      'NHKオンデマンド 月額プラン',
      'nhk-ondemand.jp',
    ],
  ],
  [
    'pixivfanbox',
    ['pixivFANBOX', 'FANBOX', 'ピクシブファンボックス', 'fanbox.cc'],
  ],
  [
    'moneyforward',
    [
      'Money Forward ME',
      'マネーフォワードME',
      'マネーフォワード ME プレミアム',
      'moneyforward.com',
    ],
  ],
  [
    'freee',
    ['freee会計', 'フリー会計', 'freee会計 スタンダード', 'freee.co.jp'],
  ],
  ['abema', ['ABEMA', 'アベマ', 'ABEMAプレミアム', 'abema.tv']],
  [
    'niconico',
    ['ニコニコ', 'ニコニコ動画', 'ニコニコプレミアム', 'nicovideo.jp'],
  ],
  ['note', ['note', 'ノート', 'noteプレミアム', 'note.com']],
] as const;

test('ADDress does not match unrelated address words or domains', async () => {
  const { resolveServiceLogo } = await import('./service-logos.ts');
  for (const name of [
    'Email Address',
    'メールアドレス',
    '住所',
    'address.com',
    'https://address.love.example.com/',
    'ADDress Consulting Ltd',
  ]) {
    assert.equal(resolveServiceLogo(name), null, name);
  }
});

for (const [slug, variants] of domesticServices) {
  test(`domestic service ${slug} uses its own local image for Japanese names, plans and official domains`, async () => {
    const { resolveServiceLogo } = await import('./service-logos.ts');
    for (const name of variants) {
      const logo = resolveServiceLogo(name);
      assert.equal(logo?.slug, slug, name);
      assert.match(logo!.src, new RegExp(`^/brand/services/${slug}\\.`));
    }
    const host = variants[variants.length - 1];
    assert.equal(resolveServiceLogo(`https://www.${host}/account`)?.slug, slug);
    assert.equal(resolveServiceLogo(`https://${host}.example.com`), null);
    assert.equal(resolveServiceLogo(`${variants[0]} Consulting Ltd`), null);
  });
}

test('domestic products retain separate logos from their parent brands', async () => {
  const { resolveServiceLogo } = await import('./service-logos.ts');
  for (const [product, parent] of [
    ['楽天マガジン', '楽天'],
    ['楽天ミュージック', '楽天'],
    ['LINE MUSIC', 'LINE'],
  ]) {
    assert.notEqual(
      resolveServiceLogo(product)?.src,
      resolveServiceLogo(parent)?.src,
      product,
    );
  }
  assert.notEqual(
    resolveServiceLogo('楽天マガジン')?.src,
    resolveServiceLogo('楽天ミュージック')?.src,
  );
});

test('domestic service-specific plan names retain their service logo', async () => {
  const { resolveServiceLogo } = await import('./service-logos.ts');
  for (const [name, slug] of [
    ['楽天ミュージック ライトプラン', 'rakutenmusic'],
    ['Rakuten Music ライトプラン', 'rakutenmusic'],
    ['radiko ダブルプラン', 'radiko'],
    ['ラジコプレミアム エリアフリープラン', 'radiko'],
    ['radikoプレミアム タイムフリー30プラン', 'radiko'],
  ])
    assert.equal(resolveServiceLogo(name)?.slug, slug, name);
});

const amazonServices = [
  [
    'amazonmusic',
    [
      'Amazon Music',
      'Amazon Music Unlimited',
      'Amazon Music Unlimited Family',
      'Amazon Music Unlimited 個人プラン',
      'Amazon Music Unlimited ワンデバイスプラン',
      'Amazon Music Prime',
      'Prime Music',
      'アマゾンミュージックアンリミテッド',
      'アマゾン ミュージック HD',
      'music.amazon.co.jp',
      'music.amazon.com',
    ],
  ],
  [
    'kindle',
    [
      'Kindle',
      'Kindle+',
      'kindle＋',
      'Kindle Plus',
      'Amazon Kindle+',
      'Kindle+ 月額プラン',
      'Kindle Plus 年額プラン',
      'キンドルプラス',
      'キンドル プラス',
      'Kindle+（旧Kindle Unlimited）',
      'Kindle Unlimited（現Kindle+）',
      'Kindle+（旧Kindle Unlimited）月額プラン',
      'Kindle Unlimited',
      'Kindle Unlimited 月額プラン',
      'キンドルアンリミテッド',
      'キンドル アンリミテッド',
    ],
  ],
  [
    'amazonkids',
    [
      'Amazon Kids+',
      'Amazon Kids Plus',
      'アマゾンキッズプラス',
      'Amazon FreeTime Unlimited',
      'Amazon Kids+ 年額プラン',
    ],
  ],
  ['amazonphotos', ['Amazon Photos', 'Amazon Photos 100GB', 'アマゾンフォト']],
  [
    'primegaming',
    [
      'Prime Gaming',
      'Amazon Prime Gaming',
      'プライムゲーミング',
      'gaming.amazon.com',
    ],
  ],
  [
    'amazonluna',
    ['Amazon Luna', 'Luna+', 'Amazon Luna Premium', 'luna.amazon.com'],
  ],
  [
    'audible',
    [
      'Audible',
      'Amazon Audible',
      'オーディブル',
      'Audible Premium Plus',
      'Audible プレミアムプラン',
    ],
  ],
  [
    'amazonprimevideo',
    [
      'Prime Video',
      'Amazon Prime Video',
      'Amazonプライムビデオ',
      'アマゾンプライムビデオ',
      'Prime Video 広告フリー',
    ],
  ],
  [
    'amazonprime',
    [
      'Amazon Prime',
      'Amazonプライム',
      'アマゾンプライム',
      'Prime Student',
      'Amazon Prime Student',
    ],
  ],
  [
    'amazonwebservices',
    ['Amazon Web Services', 'AWS', 'アマゾンウェブサービス'],
  ],
] as const;

for (const [slug, variants] of amazonServices) {
  test(`Amazon service ${slug} resolves independently with plan and Japanese variants`, async () => {
    const { resolveServiceLogo } = await import('./service-logos.ts');
    for (const name of variants) {
      assert.equal(resolveServiceLogo(name)?.slug, slug, name);
      assert.notEqual(
        resolveServiceLogo(name)?.src,
        resolveServiceLogo('Amazon')?.src,
        name,
      );
    }
    assert.equal(resolveServiceLogo(`${variants[0]} Consulting Ltd`), null);
  });
}

test('Amazon product paths preserve the product instead of always selecting the corporate logo', async () => {
  const { resolveServiceLogo } = await import('./service-logos.ts');
  for (const [url, slug] of [
    ['https://www.amazon.co.jp/music/unlimited', 'amazonmusic'],
    ['https://www.amazon.com/music/settings', 'amazonmusic'],
    ['https://www.amazon.co.jp/kindle-dbs/hz/subscribe/ku', 'kindle'],
    ['https://www.amazon.co.jp/kindle-dbs/ku', 'kindle'],
    ['https://www.amazon.co.jp/ftu/home', 'amazonkids'],
    ['https://www.amazon.co.jp/photos', 'amazonphotos'],
    ['https://www.amazon.com/amazonprime', 'amazonprime'],
    ['https://www.amazon.co.jp/primevideo', 'amazonprimevideo'],
    ['https://www.audible.co.jp/', 'audible'],
  ])
    assert.equal(resolveServiceLogo(url)?.slug, slug, url);
  for (const url of [
    'https://amazon.co.jp.evil.com/music/unlimited',
    'https://evil.com/amazon.co.jp/music/unlimited',
  ])
    assert.equal(resolveServiceLogo(url), null, url);
  assert.equal(
    resolveServiceLogo('https://www.amazon.co.jp/music-box'),
    resolveServiceLogo('Amazon'),
  );
});

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

test('major mail services resolve from IMAP endpoints and keep Japanese Yahoo distinct', async () => {
  const { resolveMailLogo } = await import('./service-logos.ts');
  for (const [host, slug] of [
    ['imap.mail.yahoo.co.jp', 'yahoojp'],
    ['imap.mail.yahoo.com', 'yahoomail'],
    ['imap.mail.me.com', 'icloudmail'],
    ['imap.aol.com', 'aolmail'],
    ['imap.zoho.com', 'zohomail'],
    ['imap.gmx.com', 'gmx'],
    ['imap.mail.com', 'maildotcom'],
  ])
    assert.equal(resolveMailLogo('imap', host)?.slug, slug, host);
  assert.notEqual(
    resolveMailLogo('imap', 'imap.mail.yahoo.co.jp')?.src,
    resolveMailLogo('imap', 'imap.mail.yahoo.com')?.src,
  );
  assert.equal(
    resolveMailLogo('imap', 'imap.mail.yahoo.co.jp.example.com'),
    null,
  );
  assert.equal(
    resolveMailLogo('gmail', 'imap.mail.yahoo.co.jp')?.slug,
    'gmail',
  );
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
