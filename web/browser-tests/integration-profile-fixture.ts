export function integrationProfileFixture() {
  let synced = false;
  let sent = false;
  return (url: URL, method: string, body: Record<string, unknown> | null) => {
    const path = '/synthetic/v1/admin/integrations';
    if (method === 'GET' && url.pathname === path)
      return Response.json({
        config: {
          version: 1,
          sender: 'sender@example.test',
          clerk_instance_id: 'ins_synthetic',
        },
        profile_ready: synced,
        profile_synced_at: synced ? '2026-11-04T00:00:00Z' : null,
        resend_ready: true,
        clerk_webhook_ready: false,
        resend_webhook_ready: false,
        attempts: [],
      });
    if (method === 'POST' && url.pathname === path + '/profile/sync') {
      if (synced || JSON.stringify(body) !== '{"expected_version":1}')
        throw Error('Unexpected profile sync body or duplicate sync');
      synced = true;
      return Response.json({
        profile_ready: true,
        profile_synced_at: '2026-11-04T00:00:00Z',
      });
    }
    if (method === 'POST' && url.pathname === path + '/test') {
      if (
        !synced ||
        sent ||
        body?.confirm !== true ||
        body.expected_version !== 1 ||
        typeof body.request_id !== 'string'
      )
        throw Error('Unexpected unconfirmed or repeated test send');
      sent = true;
      return Response.json({
        id: '11111111-1111-4111-8111-111111111111',
        request_id: body.request_id,
        state: 'accepted',
        created_at: '2026-11-04T00:00:00Z',
        sent: false,
        delivered: false,
        bounced: false,
        complained: false,
        failed: false,
      });
    }
    throw Error('Unexpected integration profile request');
  };
}
