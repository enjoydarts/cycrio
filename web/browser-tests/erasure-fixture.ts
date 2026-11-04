export function erasureFixture(mode: string) {
  let nonce: string | null = null;
  let reads = 0;
  return (url: URL, method: string, body: Record<string, unknown> | null) => {
    if (url.pathname !== '/synthetic/v1/me/withdrawal')
      throw Error('Unexpected withdrawal path');
    if (method === 'POST') {
      if (
        nonce ||
        !body ||
        Object.keys(body).sort().join(',') !==
          'confirmation,confirmed,request_id' ||
        body.confirmed !== true ||
        body.confirmation !== '退会する' ||
        typeof body.request_id !== 'string' ||
        !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(
          body.request_id,
        )
      )
        throw Error('Unexpected withdrawal confirmation or repeat POST');
      nonce = body.request_id;
      if (mode === 'erasure-uncertain')
        return Response.json(
          { error: 'dependency_unavailable' },
          { status: 503 },
        );
      return Response.json(
        { request_id: nonce, status: 'pending' },
        { status: 202 },
      );
    }
    if (
      method !== 'GET' ||
      !nonce ||
      url.searchParams.get('request_id') !== nonce ||
      [...url.searchParams.keys()].join(',') !== 'request_id'
    )
      throw Error('Unexpected withdrawal receipt query');
    reads++;
    if (reads === 1)
      return Response.json({ error: 'not_found' }, { status: 404 });
    return Response.json({ request_id: nonce, status: 'completed' });
  };
}
