import type { MailAccount } from './mail-api.ts';

export type MailServicePreset = {
  id: string;
  label: string;
  provider: MailAccount['provider'];
  host?: string;
  port?: number;
  logoSlug?: string;
  setup: string;
  helpUrl?: string;
};

// Public connection metadata only. Credentials are never part of this catalog.
export const mailServicePresets: readonly MailServicePreset[] = [
  {
    id: 'gmail',
    label: 'Gmail / Google Workspace',
    provider: 'gmail',
    logoSlug: 'gmail',
    setup: '',
  },
  {
    id: 'outlook',
    label: 'Outlook.com / Hotmail',
    provider: 'outlook',
    logoSlug: 'outlook',
    setup: '',
  },
  {
    id: 'fastmail',
    label: 'Fastmail',
    provider: 'fastmail',
    logoSlug: 'fastmail',
    setup:
      'Fastmailでメール利用を許可した専用のアプリパスワードを作成してください。',
    helpUrl:
      'https://www.fastmail.help/hc/en-us/articles/360058752854-App-passwords',
  },
  {
    id: 'yahoojp',
    label: 'Yahoo!メール（日本）',
    provider: 'imap',
    host: 'imap.mail.yahoo.co.jp',
    port: 993,
    logoSlug: 'yahoojp',
    setup:
      'Yahoo!メールの設定で「Yahoo! JAPAN公式サービス以外からのアクセス」を許可し、IMAPを有効にしてください。ログイン名はメールアドレスまたはYahoo! JAPAN ID（設定済みの場合はシークレットID）、パスワードはYahoo! JAPAN IDのパスワードを入力します。利用開始に伴うYahoo!ダイレクトオファーの登録条件も公式ヘルプで確認してください。',
    helpUrl: 'https://support.yahoo-net.jp/PccMail/s/article/H000007321',
  },
  {
    id: 'yahoo',
    label: 'Yahoo Mail（海外）',
    provider: 'imap',
    host: 'imap.mail.yahoo.com',
    port: 993,
    logoSlug: 'yahoomail',
    setup:
      '海外Yahoo Mail用です。完全なメールアドレスと、Yahooで発行したアプリパスワードを入力してください。日本のYahoo!メールとは接続先が異なります。',
    helpUrl: 'https://help.yahoo.com/kb/SLN4075.html',
  },
  {
    id: 'ocn',
    label: 'OCNメール',
    provider: 'imap',
    host: 'imap.ocn.ne.jp',
    port: 993,
    logoSlug: 'ocn',
    setup:
      '完全なメールアドレスとメールパスワードを入力してください。インターネット接続用のID・パスワードとは異なります。',
    helpUrl: 'https://support.ocn.ne.jp/personal/purpose/detail/pid2990021006/',
  },
  {
    id: 'biglobe',
    label: 'BIGLOBEメール',
    provider: 'imap',
    host: 'mail.biglobe.ne.jp',
    port: 993,
    logoSlug: 'biglobe',
    setup:
      'スタンダードプランまたはスタンダード接続契約者プランが必要です。ライトプランでは利用できません。完全なメールアドレスとBIGLOBEパスワードを入力してください。',
    helpUrl: 'https://faq.support.biglobe.ne.jp/faq_detail.html?faq_id=12403',
  },
  {
    id: 'sonet',
    label: 'So-netメール',
    provider: 'imap',
    host: 'imap.so-net.ne.jp',
    port: 993,
    logoSlug: 'sonet',
    setup:
      '有料のIMAPサービスへ事前に申し込んでください。完全なメールアドレスとメールアドレスパスワードを入力します。メールサービス（G）は対象外です。',
    helpUrl: 'https://www.so-net.ne.jp/option/mail/imap/',
  },
  {
    id: 'nifty',
    label: '@niftyメール',
    provider: 'imap',
    host: 'imap.nifty.com',
    port: 993,
    logoSlug: 'nifty',
    setup:
      'メール設定一覧で「メールソフト接続許可（IMAP）」を「許可中」にしてください。完全なメールアドレスとメールパスワードを入力します。',
    helpUrl: 'https://mail.nifty.com/cs/mail-faq/detail/200324000061/',
  },
  {
    id: 'icloudmail',
    label: 'iCloud Mail',
    provider: 'imap',
    host: 'imap.mail.me.com',
    port: 993,
    logoSlug: 'icloudmail',
    setup:
      'Apple Accountでアプリ用パスワードを作成してください。ログイン名は通常メールアドレスの@より前の部分を入力し、接続できない場合は完全なiCloudメールアドレスを入力してください。',
    helpUrl: 'https://support.apple.com/ja-jp/102525',
  },
  {
    id: 'aolmail',
    label: 'AOL Mail',
    provider: 'imap',
    host: 'imap.aol.com',
    port: 993,
    logoSlug: 'aolmail',
    setup:
      '完全なメールアドレスと、AOLのアカウント設定で発行したアプリパスワードを入力してください。',
    helpUrl:
      'https://help.aol.com/articles/how-do-i-use-other-email-applications-to-send-and-receive-my-aol-mail',
  },
  {
    id: 'zohomail',
    label: 'Zoho Mail',
    provider: 'imap',
    host: 'imap.zoho.com',
    port: 993,
    logoSlug: 'zohomail',
    setup:
      'Zoho Mailの設定でIMAPを有効にしてください。初期値は米国データセンターの個人向け設定です。地域・データセンター・契約により接続先が異なるため、Zohoに表示されるサーバー名へ変更してください。独自ドメインの有料組織では通常imappro.zoho.comを使います。2要素認証を使う場合はアプリパスワードが必要です。',
    helpUrl: 'https://www.zoho.com/mail/help/imap-access.html',
  },
  {
    id: 'gmx',
    label: 'GMX.com',
    provider: 'imap',
    host: 'imap.gmx.com',
    port: 993,
    logoSlug: 'gmx',
    setup:
      'GMX.comの設定でPOP3/IMAPアクセスを有効にしてください。完全なメールアドレスを入力し、2要素認証を使う場合は専用のアプリパスワードを使用してください。',
    helpUrl: 'https://support.gmx.com/pop-imap/imap/server.html',
  },
  {
    id: 'mailcom',
    label: 'mail.com（Premium）',
    provider: 'imap',
    host: 'imap.mail.com',
    port: 993,
    logoSlug: 'maildotcom',
    setup:
      'IMAPの利用にはmail.com Premiumが必要です。設定でPOP3/IMAPアクセスを有効にし、完全なメールアドレスを入力してください。2要素認証を使う場合は専用のアプリパスワードを使用してください。',
    helpUrl: 'https://support.mail.com/pop-imap/setting-up-pop-imap.html',
  },
  {
    id: 'imap',
    label: 'その他（IMAP）',
    provider: 'imap',
    setup:
      'メールサービスが案内するIMAPサーバー、ログイン名、パスワードを入力してください。',
  },
];

export function mailServiceForAccount(account: {
  provider: string;
  host?: string;
}): MailServicePreset {
  const custom = mailServicePresets.find((p) => p.id === 'imap')!;
  if (account.provider !== 'imap')
    return (
      mailServicePresets.find((p) => p.provider === account.provider) ?? custom
    );
  const host = account.host?.trim().toLowerCase();
  return mailServicePresets.find((p) => p.host === host && !!host) ?? custom;
}
