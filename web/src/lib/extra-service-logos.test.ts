import assert from 'node:assert/strict';
import test from 'node:test';
import { resolveServiceLogo } from './service-logos.ts';

const services: [string, string[]][] = [
  [
    'googlecloud',
    [
      'Google Cloud Platform',
      'Google Cloud',
      'GCP',
      'グーグルクラウド',
      'グーグルクラウドプラットフォーム',
      'ＧＣＰ',
      'https://cloud.google.com/',
      'https://console.cloud.google.com/billing',
    ],
  ],
  [
    'rentio',
    [
      'Rentio',
      'レンティオ',
      'Rentio 月額プラン',
      'レンティオ 月額プラン',
      'https://www.rentio.jp/',
    ],
  ],
  [
    'nijisanjifanclub',
    [
      'にじさんじ FAN CLUB',
      'にじさんじファンクラブ',
      'NIJISANJI FAN CLUB',
      'にじさんじ総合ファンクラブ',
      'にじさんじ FAN CLUB ベーシックプラン',
      'にじさんじ FAN CLUB プレミアムプラン 年額',
      'https://www.nijisanji.jp/fanclubs',
      'https://www.nijisanji.jp/fanclubs/contents/example',
      'https://event.nijisanji.app/fanclub/',
    ],
  ],
  [
    'obsidian',
    [
      'Obsidian',
      'Obsidian Sync',
      'Obsidian Sync Plus',
      'Obsidian Sync Standard',
      'オブシディアン Sync',
      'オブシディアンシンク',
      'https://obsidian.md/sync',
    ],
  ],
  [
    'onamae',
    [
      'お名前.com',
      'お名前ドットコム',
      'Onamae.com',
      'お名前.com ドメイン',
      'https://www.onamae.com/service/d/',
    ],
  ],
  ['muumuu', ['ムームードメイン', 'MuuMuu Domain', 'muumuu-domain.com']],
  [
    'valuedomain',
    ['バリュードメイン', 'VALUE DOMAIN', 'Value-Domain', 'value-domain.com'],
  ],
  [
    'xserverdomain',
    [
      'XServerドメイン',
      'エックスサーバードメイン',
      'Xserver Domain',
      'エックスドメイン',
      'xdomain.ne.jp',
    ],
  ],
  ['stardomain', ['スタードメイン', 'Star Domain', 'star-domain.jp']],
  ['gonbei', ['ゴンベエドメイン', 'Gonbei Domain', 'gonbei.jp']],
  [
    'cloudflare',
    [
      'Cloudflare Registrar',
      'クラウドフレア レジストラ',
      'https://www.cloudflare.com/products/registrar/',
    ],
  ],
  ['godaddy', ['GoDaddy', 'ゴーダディ', 'GoDaddy Domains', 'godaddy.com']],
  [
    'namecheap',
    ['Namecheap', 'ネームチープ', 'Namecheap Domains', 'namecheap.com'],
  ],
  ['porkbun', ['Porkbun', 'ポークバン', 'Porkbun Domains', 'porkbun.com']],
  ['gandi', ['Gandi', 'ガンディ', 'Gandi Domains', 'gandi.net']],
  ['dynadot', ['Dynadot', 'ダイナドット', 'dynadot.com']],
  [
    'squarespace',
    [
      'Squarespace Domains',
      'スクエアスペース ドメイン',
      'domains.squarespace.com',
    ],
  ],
];
for (const [slug, names] of services) {
  test(`${slug} resolves official services, aliases and URLs to a local logo`, () => {
    for (const name of names) {
      const logo = resolveServiceLogo(name);
      assert.equal(logo?.slug, slug, name);
      assert.match(
        logo!.src,
        /^\/(?:brand\/services|service-logos)\/[a-z0-9_.-]+$/,
      );
    }
  });
}

test('registrar aliases reject misleading names and hosts and unrelated shared-host paths', () => {
  for (const name of [
    'https://onamae.com.evil.example',
    'https://evil.example/onamae.com',
    'https://user@onamae.com',
    'お名前.com Consulting',
    'Obsidian Sync Consulting',
    'にじさんじ FAN CLUB Consulting',
    'https://www.nijisanji.jp/other/',
    'https://www.nijisanji.jp/fanclubs-fake/',
    'https://event.nijisanji.app/other/',
    'https://nijisanji.jp.evil.example/fanclubs',
    'https://evil.example/?service=Cloudflare%20Registrar',
    'Google Cloud Platform Consulting',
    'Rentio Consulting',
    'https://cloud.google.com.evil.example',
    'https://rentio.jp.evil.example',
  ])
    assert.equal(resolveServiceLogo(name), null, name);
});
