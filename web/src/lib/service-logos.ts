import { serviceLogoIndex } from './generated/service-logo-index.ts';
import { normalizeBrandName } from './service-logo-normalize.ts';
import { mailServiceForAccount } from './mail-service-presets.ts';

export type ServiceLogo = {
  slug: string;
  title: string;
  src: string;
  background?: 'dark';
};

const aliases = new Map<string, string>();
const names: [string, string[]][] = [
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
    ],
  ],
  ['youtube', ['YouTube channel membership']],
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
  [
    'googlecloud',
    [
      'Google Cloud Platform',
      'Google Cloud',
      'GCP',
      'グーグルクラウド',
      'グーグルクラウドプラットフォーム',
    ],
  ],
  ['rentio', ['Rentio', 'レンティオ']],
  [
    'nijisanjifanclub',
    [
      'にじさんじ FAN CLUB',
      'にじさんじファンクラブ',
      'NIJISANJI FAN CLUB',
      'にじさんじ総合ファンクラブ',
      'にじさんじFC',
    ],
  ],
  [
    'obsidian',
    [
      'Obsidian',
      'Obsidian Sync',
      'オブシディアン',
      'オブシディアン Sync',
      'オブシディアンシンク',
    ],
  ],
  [
    'onamae',
    ['お名前.com', 'お名前ドットコム', 'Onamae.com', 'お名前.com ドメイン'],
  ],
  ['muumuu', ['ムームードメイン', 'MuuMuu Domain']],
  ['valuedomain', ['バリュードメイン', 'VALUE DOMAIN', 'Value-Domain']],
  [
    'xserverdomain',
    [
      'XServerドメイン',
      'エックスサーバードメイン',
      'Xserver Domain',
      'エックスドメイン',
    ],
  ],
  ['stardomain', ['スタードメイン', 'Star Domain']],
  ['gonbei', ['ゴンベエドメイン', 'Gonbei Domain']],
  ['cloudflare', ['Cloudflare Registrar', 'クラウドフレア レジストラ']],
  ['godaddy', ['GoDaddy', 'ゴーダディ', 'GoDaddy Domains']],
  ['namecheap', ['Namecheap', 'ネームチープ', 'Namecheap Domains']],
  ['porkbun', ['Porkbun', 'ポークバン', 'Porkbun Domains']],
  ['gandi', ['Gandi', 'ガンディ', 'Gandi Domains']],
  ['dynadot', ['Dynadot', 'ダイナドット']],
  ['squarespace', ['Squarespace Domains', 'スクエアスペース ドメイン']],
  ['docomo', ['NTTドコモ', 'NTT DOCOMO', 'ドコモ', 'docomo']],
  ['au', ['au', 'エーユー', 'au（KDDI）']],
  ['softbank', ['SoftBank', 'ソフトバンク', 'SoftBank Mobile']],
  [
    'rakutenmobile',
    [
      '楽天モバイル',
      'Rakuten Mobile',
      '楽天モバイル 最強プラン',
      '楽天モバイル Rakuten最強プラン',
    ],
  ],
  ['ahamo', ['ahamo', 'アハモ', 'ahamo 大盛り']],
  ['povo', ['povo', 'povo2.0', 'povo1.0', 'ポヴォ']],
  ['uqmobile', ['UQ mobile', 'UQモバイル', 'ユーキューモバイル']],
  ['ymobile', ['Y!mobile', 'ワイモバイル']],
  [
    'linemo',
    ['LINEMO', 'ラインモ', 'LINEMO ベストプラン', 'LINEMO ベストプランV'],
  ],
  [
    'iijmobile',
    [
      'IIJモバイル',
      'IIJ Mobile',
      'IIJモバイルサービス',
      ...['I', 'D', 'K'].map((type) => `IIJモバイルサービス/タイプ${type}`),
    ],
  ],
  [
    'iijmio',
    [
      'IIJmio',
      'アイアイジェイミオ',
      'IIJmioモバイルサービス',
      'IIJmio ギガプラン',
    ],
  ],
  ['mineo', ['mineo', 'マイネオ', 'mineo マイピタ', 'mineo マイそく']],
  ['nihontsushin', ['日本通信SIM', '日本通信SIM 合理的シンプル290プラン']],
  ['bmobile', ['b-mobile', 'bモバイル', 'ビーモバイル']],
  ['hismobile', ['HISモバイル', 'HIS Mobile', 'エイチアイエスモバイル']],
  ['nuromobile', ['NUROモバイル', 'nuro mobile', 'ニューロモバイル']],
  ['aeonmobile', ['イオンモバイル', 'AEON Mobile']],
  ['bicsim', ['BIC SIM', 'ビックシム', 'ビックSIM']],
  ['linksmate', ['LinksMate', 'リンクスメイト']],
  ['tonemobile', ['TONEモバイル', 'トーンモバイル', 'TONE mobile']],
  [
    'x',
    [
      'The X Subscription',
      'X Subscription',
      'Xサブスクリプション',
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
    ],
  ],
  ['netflix', ['Netflix', 'ネットフリックス']],
  ['spotify', ['Spotify', 'スポティファイ']],
  [
    'youtube',
    ['YouTube', 'YouTube Premium', 'ユーチューブ', 'ユーチューブ プレミアム'],
  ],
  ['youtubemusic', ['YouTube Music', 'ユーチューブミュージック']],
  [
    'openai',
    ['OpenAI', 'ChatGPT', 'Chat GPT', 'チャットGPT', 'チャットジーピーティー'],
  ],
  ['googleone', ['Google One', 'グーグルワン']],
  ['googledrive', ['Google Drive', 'グーグルドライブ']],
  [
    'googleworkspace',
    ['Google Workspace', 'G Suite', 'Google Apps', 'グーグルワークスペース'],
  ],
  [
    'googlegemini',
    [
      'Google Gemini',
      'Gemini',
      'Gemini Advanced',
      'Google AI',
      'グーグルジェミニ',
      'ジェミニ',
    ],
  ],
  ['gmail', ['Gmail', 'Google Mail', 'ジーメール']],
  ['outlook', ['Outlook', 'Outlook.com', 'Hotmail', 'Microsoft Outlook']],
  ['fastmail', ['Fastmail', 'ファストメール']],
  ['applemusic', ['Apple Music', 'アップルミュージック']],
  ['appletv', ['Apple TV+', 'Apple TV', 'アップルTV']],
  ['apple', ['Apple', 'Apple One', 'アップルワン']],
  ['icloud', ['iCloud', 'iCloud+', 'Apple iCloud', 'アイクラウド']],
  [
    'amazonprime',
    [
      'Amazon Prime',
      'Amazonプライム',
      'アマゾンプライム',
      'アマゾン プライム',
      'Prime Student',
      'Amazon Prime Student',
    ],
  ],
  [
    'amazonprimevideo',
    [
      'Prime Video',
      'Amazon Prime Video',
      'プライムビデオ',
      'Amazonプライムビデオ',
      'アマゾンプライムビデオ',
      'Prime Video 広告フリー',
      'Amazon Prime Video 広告フリー',
    ],
  ],
  [
    'amazonwebservices',
    ['AWS', 'Amazon Web Services', 'アマゾンウェブサービス'],
  ],
  ['amazon', ['Amazon', 'アマゾン']],
  [
    'amazonmusic',
    [
      'Amazon Music',
      'Amazon Music Unlimited',
      'Amazon Music Prime',
      'Prime Music',
      'Amazon Music HD',
      'Amazon Music Unlimited Single Device',
      'Amazon Music Unlimited ワンデバイスプラン',
      'アマゾンミュージック',
      'アマゾンミュージックアンリミテッド',
      'アマゾンミュージックHD',
      'アマゾンミュージックプライム',
    ],
  ],
  [
    'kindle',
    [
      'Kindle',
      'Amazon Kindle',
      'Kindle+',
      'Kindle Plus',
      'Amazon Kindle+',
      'Kindle+（旧Kindle Unlimited）',
      'Kindle Unlimited（現Kindle+）',
      'Kindle Unlimited',
      'キンドル',
      'キンドルプラス',
      'キンドルアンリミテッド',
    ],
  ],
  [
    'amazonkids',
    [
      'Amazon Kids',
      'Amazon Kids+',
      'Amazon Kids Plus',
      'アマゾンキッズ',
      'アマゾンキッズプラス',
      'Amazon FreeTime Unlimited',
    ],
  ],
  ['amazonphotos', ['Amazon Photos', 'アマゾンフォト']],
  [
    'primegaming',
    ['Prime Gaming', 'Amazon Prime Gaming', 'プライムゲーミング'],
  ],
  ['amazonluna', ['Amazon Luna', 'Luna+']],
  ['audible', ['Audible', 'Amazon Audible', 'オーディブル']],
  [
    'microsoft365',
    [
      'Microsoft 365',
      'Office 365',
      'Microsoft Office 365',
      'Microsoft Office',
      'MS365',
      'マイクロソフト365',
      'マイクロソフト 365',
    ],
  ],
  ['onedrive', ['OneDrive', 'Microsoft OneDrive', 'ワンドライブ']],
  ['githubcopilot', ['GitHub Copilot', 'ギットハブ コパイロット']],
  ['github', ['GitHub', 'ギットハブ']],
  [
    'adobecreativecloud',
    [
      'Adobe Creative Cloud',
      'Creative Cloud',
      'Adobe CC',
      'アドビクリエイティブクラウド',
    ],
  ],
  ['adobe', ['Adobe', 'アドビ']],
  ['disneyplus', ['Disney+', 'Disney Plus', 'ディズニープラス']],
  ['hulu', ['Hulu', 'フールー']],
  ['unext', ['U-NEXT', 'ユーネクスト']],
  ['notion', ['Notion', 'ノーション']],
  ['dropbox', ['Dropbox', 'ドロップボックス']],
  ['slack', ['Slack', 'スラック']],
  ['zoom', ['Zoom', 'Zoom Workplace', 'ズーム']],
  ['discord', ['Discord', 'Discord Nitro', 'ディスコード']],
  ['canva', ['Canva', 'キャンバ']],
  ['figma', ['Figma', 'フィグマ']],
  ['claude', ['Claude', 'Anthropic', 'Anthropic Claude', 'クロード']],
  ['perplexity', ['Perplexity', 'パープレキシティ']],
  ['cursor', ['Cursor', 'カーソル']],
  ['proton', ['Proton', 'Proton Unlimited', 'プロトン']],
  ['protonmail', ['Proton Mail', 'ProtonMail', 'プロトンメール']],
  ['protonvpn', ['Proton VPN', 'ProtonVPN']],
  ['1password', ['1Password', 'ワンパスワード']],
  ['bitwarden', ['Bitwarden', 'ビットウォーデン']],
  ['nordvpn', ['NordVPN', 'ノードVPN']],
  ['expressvpn', ['ExpressVPN']],
  ['evernote', ['Evernote', 'エバーノート']],
  ['todoist', ['Todoist', 'トゥードゥイスト']],
  ['nintendo', ['Nintendo Switch Online', 'ニンテンドースイッチオンライン']],
  ['playstation', ['PlayStation Plus', 'PS Plus', 'プレイステーションプラス']],
  ['xbox', ['Xbox Game Pass', 'Game Pass', 'Xbox']],
  ['rakuten', ['楽天', 'Rakuten']],
  ['rakutenmagazine', ['楽天マガジン', 'Rakuten Magazine']],
  [
    'rakutenmusic',
    [
      '楽天ミュージック',
      'Rakuten Music',
      '楽天ミュージック ライトプラン',
      'Rakuten Music ライトプラン',
    ],
  ],
  ['line', ['LINE']],
  ['linemusic', ['LINE MUSIC', 'ラインミュージック']],
  ['dmmtv', ['DMM TV', 'DMMプレミアム', 'DMM Premium']],
  ['lemino', ['Lemino', 'レミノ']],
  ['telasa', ['TELASA', 'テラサ']],
  ['fod', ['FOD', 'FOD Premium', 'フジテレビオンデマンド']],
  ['wowow', ['WOWOW', 'WOWOWオンデマンド', 'WOWOW On Demand', 'ワウワウ']],
  [
    'radiko',
    [
      'radiko',
      'ラジコ',
      ...['radiko', 'ラジコ', 'radikoプレミアム', 'ラジコプレミアム'].flatMap(
        (name) =>
          ['エリアフリープラン', 'タイムフリー30プラン', 'ダブルプラン'].map(
            (plan) => `${name} ${plan}`,
          ),
      ),
    ],
  ],
  ['nhkondemand', ['NHKオンデマンド', 'NHK On Demand']],
  ['pixivfanbox', ['pixivFANBOX', 'FANBOX', 'ピクシブファンボックス']],
  ['note', ['note', 'ノート']],
  ['abema', ['ABEMA', 'ABEMA Premium', 'アベマ']],
  ['dazn', ['DAZN', 'ダゾーン']],
  ['danimestore', ['dアニメストア', 'd Anime Store', 'ドコモ dアニメストア']],
  ['niconico', ['ニコニコ', 'ニコニコ動画', 'ニコニコプレミアム', 'Niconico']],
  [
    'bookwalker',
    [
      'BOOK☆WALKER',
      'ブックウォーカー',
      'ブック☆ウォーカー',
      'BOOK☆WALKER 読み放題',
      'BOOK☆WALKER 読み放題 MAXコース',
      'BOOK☆WALKER 読み放題 マンガコース',
      'BOOK☆WALKER マンガ・雑誌 読み放題',
      'BOOK☆WALKER 文庫・ラノベ 読み放題',
    ],
  ],
  [
    'kakuyomu',
    [
      'カクヨム',
      'カクヨムネクスト',
      'カクヨムサポーターズパスポート',
      ...['ライト', 'スタンダード', 'デラックス'].map(
        (plan) => `カクヨムサポーターズパスポート ${plan}`,
      ),
    ],
  ],
  [
    'kadocomi',
    [
      'カドコミ',
      'ComicWalker',
      'コミックウォーカー',
      'カドコミ（コミックウォーカー）',
    ],
  ],
  ['kadokawa', ['KADOKAWA', '角川', 'カドカワ', '株式会社KADOKAWA']],
  ['freee', ['freee', 'freee会計', 'フリー会計']],
  [
    'moneyforward',
    [
      'Money Forward',
      'Money Forward ME',
      'マネーフォワード',
      'マネーフォワードME',
    ],
  ],
];
for (const [slug, variants] of names) {
  for (const name of variants) aliases.set(normalizeBrandName(name), slug);
}

const domains: Record<string, string> = {
  'techlib.jp': 'techlib',
  'techlib.co.jp': 'techlib',
  'tohotheater.jp': 'tohocinemas',
  'azure.microsoft.com': 'azure',
  'portal.azure.com': 'azure',
  'fantia.jp': 'fantia',
  'franklymail.com': 'franklymail',
  'iconpackstudio.com': 'iconpackstudio',
  'mymangomail.com': 'mangomail',
  'miraimind.ai': 'miraimind',
  'multiecube.com': 'multiecube',
  'qobuz.com': 'qobuz',
  'toho-one.com': 'tohoone',
  'mobile.unext.jp': 'unextmobile',
  'wordpress.com': 'wordpress',
  'carnavi.yahoo.co.jp': 'yahoonavi',
  'atone.be': 'atone',
  'coloria.jp': 'coloria',
  'cmoa.jp': 'cmoa',
  'dmapnavi.jp': 'zenrinnavi',
  'b-ch.com': 'bandai',
  'vandle.jp': 'vandle',
  'xtech.nikkei.com': 'nikkeixtech',
  'telnavi.jp': 'telnavi',
  'aeoncinema.com': 'aeoncinema',
  'yumobile.jp': 'yumobile',
  'justmyshop.com': 'justmyshop',
  'cloud.google.com': 'googlecloud',
  'console.cloud.google.com': 'googlecloud',
  'rentio.jp': 'rentio',
  'obsidian.md': 'obsidian',
  'onamae.com': 'onamae',
  'muumuu-domain.com': 'muumuu',
  'value-domain.com': 'valuedomain',
  'xdomain.ne.jp': 'xserverdomain',
  'star-domain.jp': 'stardomain',
  'gonbei.jp': 'gonbei',
  'cloudflare.com': 'cloudflare',
  'godaddy.com': 'godaddy',
  'namecheap.com': 'namecheap',
  'porkbun.com': 'porkbun',
  'gandi.net': 'gandi',
  'dynadot.com': 'dynadot',
  'domains.squarespace.com': 'squarespace',
  'docomo.ne.jp': 'docomo',
  'au.com': 'au',
  'softbank.jp': 'softbank',
  'network.mobile.rakuten.co.jp': 'rakutenmobile',
  'ahamo.com': 'ahamo',
  'povo.jp': 'povo',
  'ymobile.jp': 'ymobile',
  'linemo.jp': 'linemo',
  'iijmio.jp': 'iijmio',
  'mineo.jp': 'mineo',
  'nihontsushin.com': 'nihontsushin',
  'bmobile.ne.jp': 'bmobile',
  'his-mobile.com': 'hismobile',
  'mobile.nuro.jp': 'nuromobile',
  'aeonmobile.jp': 'aeonmobile',
  'bicsim.com': 'bicsim',
  'linksmate.jp': 'linksmate',
  'tone.ne.jp': 'tonemobile',
  'x.com': 'x',
  'twitter.com': 'x',
  'netflix.com': 'netflix',
  'spotify.com': 'spotify',
  'youtube.com': 'youtube',
  'music.youtube.com': 'youtubemusic',
  'openai.com': 'openai',
  'chatgpt.com': 'openai',
  'chat.openai.com': 'openai',
  'one.google.com': 'googleone',
  'drive.google.com': 'googledrive',
  'workspace.google.com': 'googleworkspace',
  'gemini.google.com': 'googlegemini',
  'gmail.com': 'gmail',
  'mail.google.com': 'gmail',
  'outlook.com': 'outlook',
  'outlook.live.com': 'outlook',
  'hotmail.com': 'outlook',
  'fastmail.com': 'fastmail',
  'music.apple.com': 'applemusic',
  'tv.apple.com': 'appletv',
  'icloud.com': 'icloud',
  'apple.com': 'apple',
  'amazon.co.jp': 'amazon',
  'amazon.com': 'amazon',
  'music.amazon.co.jp': 'amazonmusic',
  'music.amazon.com': 'amazonmusic',
  'read.amazon.co.jp': 'kindle',
  'read.amazon.com': 'kindle',
  'parents.amazon.co.jp': 'amazonkids',
  'parents.amazon.com': 'amazonkids',
  'photos.amazon.co.jp': 'amazonphotos',
  'photos.amazon.com': 'amazonphotos',
  'gaming.amazon.com': 'primegaming',
  'luna.amazon.com': 'amazonluna',
  'primevideo.com': 'amazonprimevideo',
  'aws.amazon.com': 'amazonwebservices',
  'audible.co.jp': 'audible',
  'audible.com': 'audible',
  'microsoft365.com': 'microsoft365',
  'onedrive.com': 'onedrive',
  'onedrive.live.com': 'onedrive',
  'github.com': 'github',
  'adobe.com': 'adobe',
  'disneyplus.com': 'disneyplus',
  'hulu.jp': 'hulu',
  'hulu.com': 'hulu',
  'video.unext.jp': 'unext',
  'unext.jp': 'unext',
  'animestore.docomo.ne.jp': 'danimestore',
  'magazine.rakuten.co.jp': 'rakutenmagazine',
  'music.rakuten.co.jp': 'rakutenmusic',
  'music.line.me': 'linemusic',
  'tv.dmm.com': 'dmmtv',
  'lemino.docomo.ne.jp': 'lemino',
  'telasa.jp': 'telasa',
  'fod.fujitv.co.jp': 'fod',
  'wowow.co.jp': 'wowow',
  'wod.wowow.co.jp': 'wowow',
  'radiko.jp': 'radiko',
  'nhk-ondemand.jp': 'nhkondemand',
  'fanbox.cc': 'pixivfanbox',
  'moneyforward.com': 'moneyforward',
  'freee.co.jp': 'freee',
  'abema.tv': 'abema',
  'nicovideo.jp': 'niconico',
  'bookwalker.jp': 'bookwalker',
  'global.bookwalker.jp': 'bookwalker',
  'kakuyomu.jp': 'kakuyomu',
  'comic-walker.com': 'kadocomi',
  'kadokawa.co.jp': 'kadokawa',
  'note.com': 'note',
  'notion.so': 'notion',
  'notion.com': 'notion',
  'dropbox.com': 'dropbox',
  'slack.com': 'slack',
  'zoom.us': 'zoom',
  'discord.com': 'discord',
  'canva.com': 'canva',
  'figma.com': 'figma',
  'claude.ai': 'claude',
  'anthropic.com': 'claude',
  'perplexity.ai': 'perplexity',
  'cursor.com': 'cursor',
  'cursor.sh': 'cursor',
  'proton.me': 'proton',
  '1password.com': '1password',
  'bitwarden.com': 'bitwarden',
};

function lookup(key: string): ServiceLogo | null {
  return Object.hasOwn(serviceLogoIndex, key) ? serviceLogoIndex[key] : null;
}
function brand(key: string): ServiceLogo | null {
  const slug = aliases.get(key);
  return (slug ? lookup(normalizeBrandName(slug)) : null) ?? lookup(key);
}

// Only recognizable plan qualifiers may follow a known brand. Arbitrary partial
// matches (such as "Netflix Consulting") must not become an unrelated logo.
const planSuffix =
  /^(?:(?:plus|pro|premium|standard|basic|starter|personal|family|student|individual|professional|business|enterprise|team|teams|ultimate|max|lite|essential|essentials|monthly|annual|annually|yearly|subscription|plan|プレミアム|プラス|プロ|ベーシック|スタンダード|ファミリー|パーソナル|ビジネス|個人|学生|月額|年額|月払い|年払い|年間|プラン)|\d+(?:gb|tb|人|年|ヶ月|か月|円))+$/u;

// Amazon shares its retail hostname across products. Match full path segments
// before falling back to the corporate logo; never guess from query strings.
const amazonProductPaths: [RegExp, string][] = [
  [/^\/music(?:\/|$)/i, 'amazonmusic'],
  [/^\/kindle-dbs(?:\/|$)/i, 'kindle'],
  [/^\/ftu(?:\/|$)/i, 'amazonkids'],
  [/^\/photos(?:\/|$)/i, 'amazonphotos'],
  [/^\/amazonprime(?:\/|$)/i, 'amazonprime'],
  [/^\/primevideo(?:\/|$)/i, 'amazonprimevideo'],
];

export function resolveServiceLogo(
  name: string | null | undefined,
): ServiceLogo | null {
  if (typeof name !== 'string' || !name.trim() || name.length > 512)
    return null;
  const value = name.trim();
  if (
    /^[a-z][a-z\d+.-]*:\/\//i.test(value) ||
    /^[a-z\d.-]+\.[a-z]{2,}(?:\/[^\s]*)?$/i.test(value)
  ) {
    try {
      const url = new URL(value.includes('://') ? value : `https://${value}`);
      if (
        !['http:', 'https:'].includes(url.protocol) ||
        url.username ||
        url.password ||
        url.port
      )
        return null;
      const host = url.hostname.replace(/^www\./, '');
      if (host === 'dmm.com' && /^\/rental(?:\/|$)/i.test(url.pathname))
        return lookup('dmmrental');
      if (host === 'sakura.ne.jp' && /^\/mail(?:\/|$)/i.test(url.pathname))
        return lookup('sakuramail');
      if (
        host === 'bb.excite.co.jp' &&
        /^\/exmb\/sim(?:\/|$)/i.test(url.pathname)
      )
        return lookup('excitemobile');
      if (host === 'conoha.jp' && /^\/wing(?:\/|$)/i.test(url.pathname))
        return lookup('conohawing');
      if (host === 'nijisanji.jp' && /^\/fanclubs(?:\/|$)/i.test(url.pathname))
        return lookup('nijisanjifanclub');
      if (
        host === 'event.nijisanji.app' &&
        /^\/fanclub(?:\/|$)/i.test(url.pathname)
      )
        return lookup('nijisanjifanclub');
      // These corporate hosts also serve other products; require a mobile path.
      if (
        host === 'iij.ad.jp' &&
        /^\/biz\/iijmobile(?:-dk)?(?:\/|$)/i.test(url.pathname)
      )
        return lookup('iijmobile');
      if (host === 'uqwimax.jp' && /^\/mobile(?:\/|$)/i.test(url.pathname))
        return lookup('uqmobile');
      if (host === 'amazon.co.jp' || host === 'amazon.com') {
        const product = amazonProductPaths.find(([path]) =>
          path.test(url.pathname),
        );
        if (product) return lookup(product[1]);
      }
      if (Object.hasOwn(domains, host))
        return lookup(normalizeBrandName(domains[host]));
      // Some official brand titles are domains (Chess.com, Last.fm). Only
      // their exact hostname/title pair may supplement the explicit mapping.
      const namedDomain = lookup(normalizeBrandName(host));
      return namedDomain?.title.normalize('NFKC').toLowerCase() === host
        ? namedDomain
        : null;
    } catch {
      return null;
    }
  }
  // An explicit platform marker is sufficient; never infer a creator's platform
  // from their name or accept URL-like text as a channel label.
  if (
    !/^[a-z][a-z\d+.-]*:/i.test(value) &&
    /\(\s*YouTube\s*\)$/i.test(value.normalize('NFKC'))
  )
    return lookup('youtube');
  const key = normalizeBrandName(value);
  if (/^推しドラ[-－]ホロライブ[\p{L}\p{N} ]+$/u.test(value.normalize('NFKC')))
    return lookup('yahoonavi');
  const exact = brand(key);
  if (exact) return exact;
  for (let end = key.length - 1; end >= 3; end--) {
    if (!planSuffix.test(key.slice(end))) continue;
    const matched = brand(key.slice(0, end));
    if (matched) return matched;
  }
  return null;
}

export function resolveMailLogo(
  provider: string,
  host?: string,
): ServiceLogo | null {
  const slug: Record<string, string> = {
    gmail: 'gmail',
    google: 'gmail',
    google_workspace: 'gmail',
    gmail_workspace: 'gmail',
    outlook: 'outlook',
    microsoft: 'outlook',
    hotmail: 'outlook',
    fastmail: 'fastmail',
  };
  if (Object.hasOwn(slug, provider))
    return lookup(normalizeBrandName(slug[provider]));
  const service = mailServiceForAccount({ provider, host });
  return service.logoSlug ? lookup(normalizeBrandName(service.logoSlug)) : null;
}
