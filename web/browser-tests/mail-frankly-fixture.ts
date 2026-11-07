import { account } from '../src/lib/mail-test-fixtures.ts';
import type { MailAccount } from '../src/lib/mail-api.ts';

export function mailFranklyFixture() {
  let current: MailAccount | null = null;
  const path = '/synthetic/v1/mail/accounts';
  return (url: URL, method: string, body: Record<string, unknown> | null) => {
    if (
      method === 'GET' &&
      url.pathname === '/synthetic/v1/mail/oauth/providers'
    )
      return Response.json({
        providers: [
          { provider: 'gmail', ready: false },
          { provider: 'outlook', ready: false },
        ],
      });
    if (method === 'GET' && url.pathname === path)
      return Response.json({ accounts: current ? [current] : [] });
    if (method === 'PUT' && url.pathname.startsWith(path + '/')) {
      if (
        JSON.stringify(Object.keys(body ?? {}).sort()) !==
          JSON.stringify(
            [
              'expected_version',
              'label',
              'login',
              'app_password',
              'provider',
            ].sort(),
          ) ||
        body?.provider !== 'franklymail' ||
        body.expected_version !== (current?.version ?? 0) ||
        body.label !== '合成FranklyMail' ||
        body.login !== 'synthetic@example.com' ||
        body.app_password !== 'synthetic-app-password'
      )
        throw Error('Unexpected synthetic JMAP credential request');
      current = account({
        id: url.pathname.slice(path.length + 1),
        provider: 'franklymail',
        label: String(body.label),
        version: (current?.version ?? 0) + 1,
      });
      return Response.json(current);
    }
    throw Error('Unexpected synthetic JMAP request');
  };
}
