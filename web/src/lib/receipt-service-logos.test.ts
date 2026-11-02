import assert from 'node:assert/strict';
import test from 'node:test';
import { resolveServiceLogo } from './service-logos.ts';

const services: [string, string[]][] = [
  ['techlib', ['TechLib', 'テックリブ']],
  ['tohocinemas', ['シネマイレージカード', 'シネマイレージ']],
  ['adguard', ['AdGuard License']],
  ['apple', ['AppleCare+', 'AppleCare+ 盗難・紛失プラン']],
  ['azure', ['Azure', 'Microsoft Azure']],
  ['excitemobile', ['BB.exciteモバイル', 'エキサイトモバイル']],
  ['cocchi', ['COCCHi', 'コッチ']],
  ['claude', ['Claude by Anthropic']],
  ['conohawing', ['ConoHa WING', 'ConoHa WING ベーシック']],
  ['dmmrental', ['DMM 月額レンタルサービス', 'DMM宅配レンタル']],
  ['fanzatv', ['FANZA TV', 'FANZA TV Plus']],
  ['fantia', ['Fantia', 'ファンティア']],
  ['franklymail', ['FranklyMail']],
  ['googlecloud', ['Google Cloud Platform & APIs']],
  ['iijmio', ['IIJmio モバイル']],
  ['iconpackstudio', ['Icon Pack Studio', 'IPS Pro (Icon Pack Studio)']],
  ['mangomail', ['Mango Mail', 'Mango Dashboard']],
  ['miraimind', ['MiraiMind', 'MiraiMind-君の「推し」を、君の手で']],
  ['misskey', ['MisskeyHQ']],
  ['multiecube', ['Multiecube', 'マルチエキューブ']],
  ['mykirin', ['My KIRIN']],
  ['qobuz', ['Qobuz']],
  ['tohoone', ['TOHO-ONE']],
  ['unextmobile', ['U-NEXT MOBILE']],
  ['wordpress', ['WordPress.com']],
  [
    'yahoonavi',
    [
      'Yahoo!カーナビ',
      'Yahoo!カーナビプラス',
      'Yahoo!カーナビ - 広い道を優先案内、渋滞情報',
      '推しドラ-ホロライブExample',
    ],
  ],
  [
    'youtube',
    [
      'YouTube channel membership',
      'Example Channel メンバーシップ (YouTube)',
      'Example Channel (YouTube)',
      'Example Channel （YouTube）',
    ],
  ],
  ['atone', ['atone']],
  ['docomo', ['d Wi-Fi']],
  ['onamae', ['お名前.com ドメイン登録']],
  ['sakuramail', ['さくらのメールボックス']],
  ['amemil', ['アメミル']],
  ['coloria', ['カラリア', 'カラリア 香りの定期便']],
  ['cmoa', ['コミックシーモア']],
  [
    'zenrinnavi',
    ['ゼンリン地図ナビ', 'ゼンリン地図ナビ-カーナビ/住宅地図/地図アプリ'],
  ],
  ['niconico', ['ニコニコチャンネル']],
  ['norton', ['ノートン', 'ノートン™ IDアドバイザー']],
  ['bandai', ['バンダイチャンネル']],
  ['vandle', ['ポチっとチャージ', 'バンドルカード']],
  ['suica', ['モバイルSuica会員', 'モバイルSuica']],
  [
    'yumobile',
    [
      'y.u mobile',
      'y.u mobile サービス利用料',
      'ワイユ-モバイル',
      'ワイユ-モバイル サ-ビスリヨウリヨウ',
    ],
  ],
  ['aeoncinema', ['ワタシアタープラス', 'ワタシアター会員サービス']],
  ['wagakki', ['和楽器バンド Official Website']],
  ['nikkeixtech', ['日経クロステック']],
  ['matsuben', ['松弁ネット']],
  [
    'telnavi',
    [
      '電話帳ナビ',
      '電話帳ナビ-迷惑電話と迷惑メッセージを自動判別で強力ブロック',
    ],
  ],
  ['gmobb', ['GMOとくとくBB', 'WiMAX +5G ギガ放題接続サービス']],
  ['justmyshop', ['Just MyShop']],
];

for (const [slug, names] of services) {
  test(`${slug} identifies explicit receipt service names with a local logo`, () => {
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

test('unidentified purchases and ambiguous names retain the generic icon', () => {
  for (const name of [
    '',
    'Domain Renewal',
    'Whois情報公開代行',
    'サービス維持調整費',
    'シリーズ購読',
    '月額レンタルサービス',
    'Unknown Channel',
    'Unknown Example Consulting',
    'example.invalid domain registration',
    'https://evil.example/YouTube',
    'https://evil.example/ (YouTube)',
    'javascript:alert(1) (YouTube)',
    'Rentio Consulting',
    'COCCHi Consulting',
  ]) {
    assert.equal(resolveServiceLogo(name), null, name);
  }
});
