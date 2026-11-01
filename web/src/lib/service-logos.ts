import { serviceLogoIndex } from './generated/service-logo-index.ts';
import { normalizeBrandName } from './service-logo-normalize.ts';

export type ServiceLogo = {
  slug: string;
  title: string;
  src: string;
  background?: 'dark';
};

const aliases = new Map<string, string>();
const names: [string, string[]][] = [
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
      'Kindle Unlimited',
      'キンドル',
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
  const key = normalizeBrandName(value);
  const exact = brand(key);
  if (exact) return exact;
  for (let end = key.length - 1; end >= 3; end--) {
    if (!planSuffix.test(key.slice(end))) continue;
    const matched = brand(key.slice(0, end));
    if (matched) return matched;
  }
  return null;
}

export function resolveMailLogo(provider: string): ServiceLogo | null {
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
  return Object.hasOwn(slug, provider)
    ? lookup(normalizeBrandName(slug[provider]))
    : null;
}
