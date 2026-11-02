import assert from 'node:assert/strict';
import test from 'node:test';
import { resolveServiceLogo } from './service-logos.ts';

const mobileServices: [string, string[]][] = [
  ['docomo', ['NTTドコモ', 'NTT DOCOMO', 'ドコモ', 'docomo', 'docomo.ne.jp']],
  ['au', ['au', 'エーユー', 'au（KDDI）', 'au.com']],
  ['softbank', ['SoftBank', 'ソフトバンク', 'SoftBank Mobile', 'softbank.jp']],
  [
    'rakutenmobile',
    [
      '楽天モバイル',
      'Rakuten Mobile',
      '楽天モバイル 最強プラン',
      'network.mobile.rakuten.co.jp',
    ],
  ],
  ['ahamo', ['ahamo', 'アハモ', 'ahamo 大盛り', 'ahamo.com']],
  ['povo', ['povo', 'povo2.0', 'povo 1.0', 'ポヴォ', 'povo.jp']],
  [
    'uqmobile',
    [
      'UQ mobile',
      'UQモバイル',
      'ユーキューモバイル',
      'https://www.uqwimax.jp/mobile/plan/',
    ],
  ],
  ['ymobile', ['Y!mobile', 'ワイモバイル', 'Ｙ！ｍｏｂｉｌｅ', 'ymobile.jp']],
  ['linemo', ['LINEMO', 'ラインモ', 'LINEMO ベストプラン', 'linemo.jp']],
  [
    'iijmobile',
    [
      'IIJモバイル',
      'IIJ Mobile',
      'IIJモバイルサービス',
      'IIJモバイルサービス/タイプI',
      'IIJモバイルサービス/タイプD',
      'IIJモバイルサービス/タイプK',
      'https://www.iij.ad.jp/biz/iijmobile/',
      'https://www.iij.ad.jp/biz/iijmobile-dk/',
    ],
  ],
  [
    'iijmio',
    [
      'IIJmio',
      'アイアイジェイミオ',
      'IIJmioモバイルサービス',
      'IIJmio ギガプラン',
      'iijmio.jp',
    ],
  ],
  [
    'mineo',
    ['mineo', 'マイネオ', 'mineo マイピタ', 'mineo マイそく', 'mineo.jp'],
  ],
  [
    'nihontsushin',
    [
      '日本通信SIM',
      '日本通信 Sim',
      '日本通信SIM 合理的シンプル290プラン',
      'nihontsushin.com',
    ],
  ],
  ['bmobile', ['b-mobile', 'bモバイル', 'ビーモバイル', 'bmobile.ne.jp']],
  [
    'hismobile',
    ['HISモバイル', 'HIS Mobile', 'エイチアイエスモバイル', 'his-mobile.com'],
  ],
  [
    'nuromobile',
    ['NUROモバイル', 'nuro mobile', 'ニューロモバイル', 'mobile.nuro.jp'],
  ],
  ['aeonmobile', ['イオンモバイル', 'AEON Mobile', 'aeonmobile.jp']],
  ['bicsim', ['BIC SIM', 'ビックシム', 'ビックSIM', 'bicsim.com']],
  ['linksmate', ['LinksMate', 'リンクスメイト', 'linksmate.jp']],
  [
    'tonemobile',
    ['TONEモバイル', 'トーンモバイル', 'TONE mobile', 'tone.ne.jp'],
  ],
];

for (const [slug, names] of mobileServices) {
  test(`mobile service ${slug} resolves names and official URLs to a local image`, () => {
    for (const name of names) {
      const logo = resolveServiceLogo(name);
      assert.equal(logo?.slug, slug, name);
      assert.match(logo!.src, new RegExp(`^/brand/services/${slug}\\.`));
    }
  });
}

test('mobile brands stay separate and never match unrelated services or misleading hosts', () => {
  for (const [a, b] of [
    ['IIJモバイル', 'IIJmio'],
    ['au', 'UQ mobile'],
    ['au', 'povo'],
    ['SoftBank', 'Y!mobile'],
    ['SoftBank', 'LINEMO'],
    ['楽天モバイル', '楽天マガジン'],
    ['NTTドコモ', 'ahamo'],
  ]) {
    assert.notEqual(resolveServiceLogo(a)?.src, resolveServiceLogo(b)?.src);
  }
  for (const name of [
    'https://www.uqwimax.jp/wimax/',
    'https://www.iij.ad.jp/biz/other/',
    'https://www.iij.ad.jp/biz/iijmobile-fake/',
    'https://evil.example/?service=IIJmio',
    'https://iijmio.jp.evil.example',
    'https://evil@mineo.jp',
    'IIJmio Consulting',
    'au Consulting',
    '日本通信SIM Consulting',
  ]) {
    assert.equal(resolveServiceLogo(name), null, name);
  }
});

test('The X Subscription resolves to X with spelling variants and plan qualifiers', () => {
  for (const name of [
    'The X Subscription',
    'the x subscription',
    'Ｔｈｅ Ｘ Ｓｕｂｓｃｒｉｐｔｉｏｎ',
    'X Subscription',
    'X サブスクリプション',
    'The X Subscription Premium+',
    'The X Subscription 月額プラン',
  ]) {
    assert.equal(resolveServiceLogo(name)?.slug, 'x', name);
    assert.equal(resolveServiceLogo(name)?.src, '/service-logos/x.svg', name);
  }
  for (const name of [
    'The X Subscription Consulting',
    'Unknown X Subscription',
    'Subscription',
  ])
    assert.equal(resolveServiceLogo(name), null, name);
});

test('X paid tiers and legacy Twitter Blue use the X logo without broad single-letter matches', () => {
  for (const name of [
    'X Premium',
    'X Premium+',
    'X Premium Plus',
    'X Basic',
    'Xプレミアム',
    'Xプレミアムプラス',
    'X ベーシック',
    'Twitter Blue',
    'Twitterブルー',
    'ツイッターブルー',
    'X（旧Twitter） Premium',
    'https://x.com/i/premium_sign_up',
    'https://twitter.com/i/twitter_blue_sign_up',
  ]) {
    const logo = resolveServiceLogo(name);
    assert.equal(logo?.slug, 'x', name);
    assert.equal(logo.src, '/service-logos/x.svg');
  }
  for (const name of [
    'X Consulting',
    'X Premium Consulting',
    'Premium',
    'Basic',
    'https://x.com.evil.example',
    'https://evil.example/x.com',
  ]) {
    assert.equal(resolveServiceLogo(name), null, name);
  }
});
