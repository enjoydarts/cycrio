import assert from 'node:assert/strict';

// Runs against the fixture origin. The real SDK is used; ingest is intercepted.
// Optional live options are passed through WebDriver arguments, never a URL/log.
export async function browserTelemetryProbe(options = {}) {
  const originalFetch = window.fetch;
  // The fixture replaces window.fetch with its synthetic API. An isolated
  // about:blank realm supplies the browser transport for an explicit live probe.
  const transportFrame = options.live ? document.createElement('iframe') : null;
  if (transportFrame) {
    transportFrame.hidden = true;
    transportFrame.setAttribute('aria-hidden', 'true');
    document.body.append(transportFrame);
  }
  const liveFetch = transportFrame?.contentWindow?.fetch.bind(
    transportFrame.contentWindow,
  );
  const beforeCookie = document.cookie;
  const beforeLocal = Object.keys(localStorage).sort();
  const beforeSession = Object.keys(sessionStorage).sort();
  const requests = [];
  const events = [];
  const statuses = [];
  const token = options.projectToken ?? 'phc_synthetic_browser_token';
  const host = options.host ?? 'https://us.i.posthog.com';
  let controller;
  let sdk;
  try {
    window.fetch = async (resource, init) => {
      const url = new URL(String(resource), location.href);
      if (url.origin !== host) return originalFetch(resource, init);
      const body =
        init?.body instanceof Blob
          ? await init.body.text()
          : String(init?.body ?? '');
      requests.push({ path: url.pathname, query: url.search, body });
      if (options.live) {
        if (!liveFetch) throw new Error('Native browser transport unavailable');
        const response = await liveFetch(resource, init);
        statuses.push(response.status);
        return response;
      }
      return Response.json({ status: 1 });
    };
    const { createTelemetryController } = await import('/src/lib/telemetry.ts');
    const { PostHog } =
      await import('/node_modules/posthog-js/dist/module.mjs');
    controller = createTelemetryController({
      production: true,
      target: window,
      origin: location.origin,
      getAssetPaths: () => new Set(['/_app/immutable/chunks/synthetic.js']),
      getConfiguration: async () => ({
        enabled: true,
        project_token: token,
        host,
      }),
      loadSDK: async () => ({
        initialize(projectToken, config) {
          const filter = config.before_send;
          sdk = new PostHog().init(projectToken, {
            ...config,
            opt_out_useragent_filter: true,
            disable_compression: true,
            api_transport: 'fetch',
            before_send(event) {
              const safe = filter(event);
              if (safe) events.push(safe);
              return safe;
            },
          });
          return {
            capture: (_event, properties) =>
              sdk.captureException(
                new Error('Unhandled application error'),
                properties,
              ),
            reset: () => sdk.reset(),
            disable: () => sdk.set_config({ before_send: () => null }),
          };
        },
      }),
    });
    controller.start();
    for (let i = 0; i < 100 && !sdk; i++)
      await new Promise((resolve) => setTimeout(resolve, 20));
    if (!sdk) throw Error('SDK initialization timed out');
    const owner = '11111111-1111-4111-8111-111111111111';
    controller.setUser(owner);
    const error = new Error('private@example.test secret-token');
    error.stack = `Error: private@example.test\n at privateFunction (${location.origin}/_app/immutable/chunks/synthetic.js?email=private:42:7)\n at private (${location.origin}/subscriptions/private-identifier:4:2)`;
    window.dispatchEvent(
      new ErrorEvent('error', {
        error,
        message: error.message,
        filename: location.href,
      }),
    );
    controller.capture(error);
    window.dispatchEvent(
      new PromiseRejectionEvent('unhandledrejection', {
        promise: Promise.resolve(),
        reason: error,
      }),
    );
    sdk.capture('$pageview', {
      $current_url: location.href,
      email: 'private@example.test',
    });
    controller.setUser(null);
    controller.capture('raw rejection private@example.test');
    for (
      let i = 0;
      i < 500 && (requests.length < 2 || (options.live && statuses.length < 2));
      i++
    )
      await new Promise((resolve) => setTimeout(resolve, 20));
    controller.dispose();
    controller.capture(new Error('disposed'));
    return {
      events: events.map((event) => ({
        ...event,
        properties: { ...event.properties, token: '[project-token]' },
      })),
      // Keep the public token out of artifacts even in a live probe.
      requests: requests.map((request) => ({
        ...request,
        body: request.body.split(token).join('[project-token]'),
      })),
      statuses,
      fixtureErrors: window.browserTest?.errors ?? [],
      cookieUnchanged: document.cookie === beforeCookie,
      localUnchanged:
        JSON.stringify(Object.keys(localStorage).sort()) ===
        JSON.stringify(beforeLocal),
      sessionUnchanged:
        JSON.stringify(Object.keys(sessionStorage).sort()) ===
        JSON.stringify(beforeSession),
    };
  } finally {
    controller?.dispose();
    window.fetch = originalFetch;
    transportFrame?.remove();
  }
}

export async function runTelemetryProbe(api, session, options = {}) {
  const result = await api('POST', session + '/execute/async', {
    script: `const done=arguments[arguments.length-1]; (${browserTelemetryProbe.toString()})(arguments[0]).then(done,error=>done({error:String(error)}));`,
    args: [options],
  });
  assert.equal(result.error, undefined, JSON.stringify(result));
  assert.equal(result.events.length, 2, JSON.stringify(result));
  assert.equal(
    result.requests.length,
    2,
    JSON.stringify({
      statuses: result.statuses,
      fixtureErrors: result.fixtureErrors,
    }),
  );
  assert.equal(result.cookieUnchanged, true);
  assert.equal(result.localUnchanged, true);
  assert.equal(result.sessionUnchanged, true);
  if (options.live) assert.deepEqual(result.statuses, [200, 200]);
  for (const event of result.events) {
    assert.equal(event.event, '$exception');
    assert.deepEqual(
      Object.keys(event.properties).sort(),
      [
        '$exception_list',
        '$process_person_profile',
        '$geoip_disable',
        'runtime',
        '$lib',
        '$lib_version',
        'distinct_id',
        'token',
      ].sort(),
    );
  }
  assert.equal(
    result.events[0].properties.distinct_id,
    '11111111-1111-4111-8111-111111111111',
  );
  assert.equal(
    result.events[1].properties.distinct_id,
    'cycrio-browser-anonymous',
  );
  assert.doesNotMatch(
    JSON.stringify(result),
    /private@example|private-identifier|secret-token|privateFunction|\$current_url|\$device_id|\$session_id|\$referrer/,
  );
  for (const request of result.requests) {
    assert.ok(['/i/v0/', '/e/'].includes(request.path), request.path);
    const payload = JSON.parse(request.body);
    const data = payload.batch?.[0] ?? payload;
    assert.equal(data.event, '$exception', JSON.stringify(payload));
    assert.deepEqual(
      Object.keys(data.properties).sort(),
      [
        '$exception_list',
        '$process_person_profile',
        '$geoip_disable',
        'runtime',
        '$lib',
        '$lib_version',
        'distinct_id',
        'token',
      ].sort(),
    );
    assert.equal(data.properties.$geoip_disable, true);
  }
  return {
    scenario: options.live ? 'telemetry-sdk-live' : 'telemetry-sdk-wire',
    status: 'pass',
    ...(options.live ? { statuses: result.statuses } : {}),
  };
}
