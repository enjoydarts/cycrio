import { credentialProviders, type CredentialSummary } from '../src/lib/api.ts';

export function healthchecksFixture() {
  let current: CredentialSummary = {
    provider: 'healthchecks',
    state: 'not_configured',
    version: 0,
    updated_at: null,
  };
  const path = '/synthetic/v1/admin/credentials';
  return (url: URL, method: string, body: Record<string, unknown> | null) => {
    if (method === 'GET' && url.pathname === '/synthetic/v1/admin/audit')
      return Response.json({ events: [] });
    if (method === 'GET' && url.pathname === path)
      return Response.json({
        credentials: credentialProviders.map((provider) =>
          provider === 'healthchecks'
            ? current
            : {
                provider,
                state: 'not_configured',
                version: 0,
                updated_at: null,
              },
        ),
      });
    if (
      url.pathname === `${path}/healthchecks` &&
      ['PUT', 'DELETE'].includes(method)
    ) {
      if (body?.expected_version !== current.version)
        throw Error('Unexpected healthchecks version');
      if (
        method === 'PUT' &&
        body.secret !==
          'https://hc-ping.com/11111111-1111-4111-8111-111111111111'
      )
        throw Error('Unexpected synthetic Ping URL');
      current = {
        ...current,
        state: method === 'PUT' ? 'configured' : 'disabled',
        version: current.version + 1,
        updated_at: '2026-11-06T00:00:00Z',
      };
      return Response.json(current);
    }
    throw Error('Unexpected healthchecks UI request');
  };
}
