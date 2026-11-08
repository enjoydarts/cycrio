import assert from 'node:assert/strict';

// Uses the actual SDK serializer and both browser transports, without ingest
// access unless the caller explicitly supplies live configuration.
export async function browserPageTelemetryProbe(options = {}) {
  const originalFetch = window.fetch;
  const originalBeacon = navigator.sendBeacon;
  const frame = options.live ? document.createElement('iframe') : null;
  if (frame) {
    frame.hidden = true;
    document.body.append(frame);
  }
  const liveFetch = frame?.contentWindow?.fetch.bind(frame.contentWindow);
  const snapshot = () => ({
    cookie: document.cookie,
    local: Object.entries(localStorage).sort(),
    session: Object.entries(sessionStorage).sort(),
  });
  const before = snapshot();
  const requests = [];
  const pending = [];
  const statuses = [];
  const events = [];
  const token = options.projectToken ?? 'phc_synthetic_browser_page_token';
  const host = options.host ?? 'https://us.i.posthog.com';
  let sdk;
  let controller;
  let initialized = false;
  let sessionID;
  const record = async (resource, init, transport) => {
    const index = requests.length;
    requests.push(null);
    const url = new URL(String(resource), location.href);
    let body =
      init?.body instanceof Blob
        ? await init.body.text()
        : String(init?.body ?? '');
    // sendBeacon uses a form-encoded, base64 JSON envelope even with SDK
    // compression disabled. Decode before validation and token redaction.
    if (body.startsWith('data=')) {
      const encoded = new URLSearchParams(body).get('data');
      body = new TextDecoder().decode(
        Uint8Array.from(atob(encoded), (character) => character.charCodeAt(0)),
      );
    }
    requests[index] = {
      path: url.pathname,
      query: url.search,
      body,
      transport,
    };
    if (options.live) {
      if (!liveFetch) throw Error('Native browser transport unavailable');
      const response = await liveFetch(resource, init);
      statuses.push(response.status);
      return response;
    }
    return Response.json({ status: 1 });
  };
  try {
    window.fetch = (resource, init) => {
      if (new URL(String(resource), location.href).origin !== host)
        return originalFetch(resource, init);
      const request = record(resource, init, 'fetch');
      pending.push(request);
      return request;
    };
    navigator.sendBeacon = (resource, body) => {
      if (new URL(String(resource), location.href).origin !== host)
        return originalBeacon.call(navigator, resource, body);
      pending.push(record(resource, { method: 'POST', body }, 'sendBeacon'));
      return true;
    };
    const { createTelemetryController } = await import('/src/lib/telemetry.ts');
    const { PostHog } =
      await import('/node_modules/posthog-js/dist/module.mjs');
    controller = createTelemetryController({
      production: true,
      target: window,
      origin: location.origin,
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
          sessionID = sdk.get_session_id();
          initialized = true;
          return {
            captureException: (error, properties) =>
              sdk.captureException(error, properties),
            capturePage: (event, properties) =>
              sdk.capture(
                event,
                properties,
                event === '$pageleave'
                  ? { transport: 'sendBeacon', send_instantly: true }
                  : undefined,
              ),
            getSessionId: () => sdk.get_session_id(),
            reset: () => sdk.reset(),
            disable: () => sdk.set_config({ before_send: () => null }),
          };
        },
      }),
    });
    controller.start();
    for (let i = 0; i < 100 && !initialized; i++)
      await new Promise((resolve) => setTimeout(resolve, 20));
    if (!initialized) throw Error('SDK initialization timed out');
    controller.navigate('/subscriptions');
    controller.navigate('/');
    // Invalid inputs and unsolicited SDK calls must not escape the boundary.
    sdk.capture('$pageview', {
      $current_url: location.href,
      email: 'private@example.test',
    });
    controller.setUser('11111111-1111-4111-8111-111111111111');
    controller.navigate('/subscriptions?email=private@example.test');
    controller.navigate('/subscriptions#secret-token');
    controller.navigate('/subscriptions/22222222-2222-4222-8222-222222222222');
    controller.navigate('/subscriptions/22222222-2222-4222-8222-222222222222');
    window.dispatchEvent(
      new PageTransitionEvent('pagehide', { persisted: true }),
    );
    window.dispatchEvent(
      new PageTransitionEvent('pagehide', { persisted: true }),
    );
    window.dispatchEvent(
      new PageTransitionEvent('pageshow', { persisted: true }),
    );
    window.dispatchEvent(
      new PageTransitionEvent('pageshow', { persisted: true }),
    );
    window.dispatchEvent(
      new PageTransitionEvent('pagehide', { persisted: false }),
    );
    controller.dispose();
    controller.dispose();
    controller.navigate('/');
    for (let i = 0; i < 100 && requests.length < events.length; i++)
      await new Promise((resolve) => setTimeout(resolve, 20));
    await Promise.all(pending);
    const after = snapshot();
    return {
      events: events.map((event) => ({
        ...event,
        properties: { ...event.properties, token: '[project-token]' },
      })),
      requests: requests.map((request) => ({
        ...request,
        body: request.body.split(token).join('[project-token]'),
      })),
      statuses,
      origin: location.origin,
      sessionID,
      cookieUnchanged: before.cookie === after.cookie,
      localUnchanged:
        JSON.stringify(before.local) === JSON.stringify(after.local),
      sessionUnchanged:
        JSON.stringify(before.session) === JSON.stringify(after.session),
    };
  } finally {
    controller?.dispose();
    window.fetch = originalFetch;
    navigator.sendBeacon = originalBeacon;
    frame?.remove();
  }
}

export async function runPageTelemetryProbe(api, session, options = {}) {
  const result = await api('POST', session + '/execute/async', {
    script: `const done=arguments[arguments.length-1]; (${browserPageTelemetryProbe.toString()})(arguments[0]).then(done,error=>done({error:String(error)}));`,
    args: [options],
  });
  assert.equal(result.error, undefined, JSON.stringify(result));
  assert.equal(result.cookieUnchanged, true);
  assert.equal(result.localUnchanged, true);
  assert.equal(result.sessionUnchanged, true);
  const expected = [
    ['$pageview', '/'],
    ['$pageleave', '/'],
    ['$pageview', '/'],
    ['$pageleave', '/'],
    ['$pageview', '/subscriptions/detail'],
    ['$pageleave', '/subscriptions/detail'],
    ['$pageview', '/subscriptions/detail'],
    ['$pageleave', '/subscriptions/detail'],
  ];
  assert.deepEqual(
    result.events.map(({ event, properties }) => [event, properties.$pathname]),
    expected,
  );
  const payloads = result.requests.flatMap((request) => {
    assert.ok(['/i/v0/', '/e/'].includes(request.path), request.path);
    const payload = JSON.parse(request.body);
    return (payload.batch ?? [payload]).map((event) => ({
      ...event,
      transport: request.transport,
    }));
  });
  assert.equal(payloads.length, expected.length, JSON.stringify(result));
  assert.deepEqual(
    payloads.map(({ event, properties }) => [event, properties.$pathname]),
    expected,
  );
  assert.deepEqual(
    payloads.map(({ properties }) => properties),
    result.events.map(({ properties }) => properties),
    'The SDK wire must preserve the exact filtered properties and identifiers',
  );
  const uuid =
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
  const allowed = new Set([
    '$current_url',
    '$host',
    '$pathname',
    'page_name',
    '$session_id',
    '$pageview_id',
    '$process_person_profile',
    '$geoip_disable',
    'runtime',
    'distinct_id',
    'token',
    '$lib',
    '$lib_version',
  ]);
  const leaveKeys = [
    '$prev_pageview_duration',
    '$prev_pageview_pathname',
    '$prev_pageview_id',
    '$prev_pageview_host',
  ];
  const pageIDs = new Set();
  for (const collection of [result.events, payloads]) {
    for (const [index, data] of collection.entries()) {
      const p = data.properties;
      const isLeave = data.event === '$pageleave';
      for (const key of Object.keys(p))
        assert.ok(
          allowed.has(key) || (isLeave && leaveKeys.includes(key)),
          key,
        );
      assert.equal(p.$current_url, result.origin + p.$pathname);
      assert.equal(p.$host, new URL(result.origin).host);
      assert.equal(
        p.page_name,
        p.$pathname === '/' ? 'home' : 'subscription_detail',
      );
      assert.match(p.$session_id, uuid);
      assert.match(p.$pageview_id, uuid);
      assert.equal(p.$geoip_disable, true);
      assert.equal(p.$process_person_profile, false);
      assert.equal(p.runtime, 'browser');
      if (index < 2) {
        assert.match(p.distinct_id, /^cycrio-visitor-/);
        assert.match(p.distinct_id.slice('cycrio-visitor-'.length), uuid);
        assert.equal(p.distinct_id, collection[0].properties.distinct_id);
      } else
        assert.equal(p.distinct_id, '11111111-1111-4111-8111-111111111111');
      if (isLeave) {
        assert.equal(
          p.$pageview_id,
          collection[index - 1].properties.$pageview_id,
        );
        assert.equal(p.$prev_pageview_id, p.$pageview_id);
        assert.equal(p.$prev_pageview_pathname, p.$pathname);
        assert.equal(p.$prev_pageview_host, p.$host);
        assert.equal(typeof p.$prev_pageview_duration, 'number');
        assert.ok(
          p.$prev_pageview_duration >= 0 && p.$prev_pageview_duration <= 86400,
        );
      }
    }
  }
  for (const event of result.events.filter(
    (event) => event.event === '$pageview',
  )) {
    assert.ok(
      !pageIDs.has(event.properties.$pageview_id),
      'Each view needs a new ID',
    );
    pageIDs.add(event.properties.$pageview_id);
  }
  assert.equal(result.events[0].properties.$session_id, result.sessionID);
  assert.equal(
    payloads.filter(
      (event) =>
        event.event === '$pageleave' && event.transport === 'sendBeacon',
    ).length,
    4,
  );
  assert.doesNotMatch(
    JSON.stringify(result),
    /private@example|secret-token|22222222-2222|\$referrer|\$device_id|\$browser|\$os|\$screen|\$title|mode=healthchecks/,
  );
  if (options.live) {
    assert.equal(result.statuses.length, expected.length);
    assert.ok(result.statuses.every((status) => status === 200));
  }
  return {
    scenario: options.live
      ? 'telemetry-pages-sdk-live'
      : 'telemetry-pages-sdk-wire',
    status: 'pass',
    ...(options.live ? { statuses: result.statuses } : {}),
  };
}

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
  const oldChunkIds = window._posthogChunkIds;
  const syntheticAsset =
    location.origin + '/_app/immutable/chunks/synthetic.js';
  const syntheticChunkId = '11111111-2222-4333-8444-555555555555';
  window._posthogChunkIds = {
    ...oldChunkIds,
    [`Error\n at injectChunk (${syntheticAsset}:1:1)`]: syntheticChunkId,
  };
  const token = options.projectToken ?? 'phc_synthetic_browser_token';
  const host = options.host ?? 'https://us.i.posthog.com';
  let controller;
  let sdk;
  let beforeSend;
  let nativeFrameFields;
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
      getAssetPaths: () => new Set([syntheticAsset]),
      getConfiguration: async () => ({
        enabled: true,
        project_token: token,
        host,
      }),
      loadSDK: async () => ({
        initialize(projectToken, config) {
          const filter = config.before_send;
          beforeSend = (event) => {
            const safe = filter(event);
            if (safe) events.push(safe);
            return safe;
          };
          sdk = new PostHog().init(projectToken, {
            ...config,
            opt_out_useragent_filter: true,
            disable_compression: true,
            api_transport: 'fetch',
            before_send: beforeSend,
          });
          return {
            captureException: (error, properties) =>
              sdk.captureException(error, properties),
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
    // Compare the native SDK schema without sending this diagnostic event.
    sdk.set_config({
      before_send(event) {
        nativeFrameFields = Object.keys(
          event.properties.$exception_list[0].stacktrace.frames[0],
        ).sort();
        return null;
      },
    });
    const schemaError = new Error('Synthetic schema error');
    schemaError.stack = `Error: Synthetic schema error\n at syntheticFunction (${location.origin}/_app/immutable/chunks/synthetic.js:42:7)`;
    sdk.captureException(schemaError);
    sdk.set_config({ before_send: beforeSend });
    const owner = '11111111-1111-4111-8111-111111111111';
    controller.setUser(owner);
    const error = new TypeError(
      `Cannot read properties of undefined (reading 'foo') private@example.test Bearer synthetic-secret-token\n at forgedMessageFunction (${syntheticAsset}:999:999)\nCause: diagnostic render path`,
    );
    error.stack = `TypeError: ${error.message}\n at loadSubscription (${syntheticAsset}?email=private:42:7)\n at private (${location.origin}/subscriptions/private-identifier:4:2)`;
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
      nativeFrameFields,
      asset: syntheticAsset,
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
    if (oldChunkIds === undefined) delete window._posthogChunkIds;
    else window._posthogChunkIds = oldChunkIds;
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
    const allowed = new Set([
      '$exception_list',
      '$process_person_profile',
      '$geoip_disable',
      'runtime',
      '$lib',
      '$lib_version',
      'distinct_id',
      'token',
      '$exception_level',
      '$browser',
      '$browser_version',
      '$os',
      '$os_version',
      '$device_type',
    ]);
    for (const key of Object.keys(event.properties))
      assert.ok(allowed.has(key), key);
  }
  assert.equal(
    result.events[0].properties.distinct_id,
    '11111111-1111-4111-8111-111111111111',
  );
  assert.equal(
    result.events[1].properties.distinct_id,
    'cycrio-browser-anonymous',
  );
  const stacktrace = result.events[0].properties.$exception_list[0].stacktrace;
  assert.equal(
    result.events[0].properties.$exception_list[0].type,
    'TypeError',
  );
  assert.match(
    result.events[0].properties.$exception_list[0].value,
    /Cannot read properties of undefined \(reading 'foo'\)/,
  );
  assert.match(
    result.events[0].properties.$exception_list[0].value,
    /Cause: diagnostic render path/,
  );
  assert.equal(
    result.events[0].properties.$exception_list[0].mechanism.type,
    'generic',
  );
  assert.equal(stacktrace.type, 'raw');
  assert.deepEqual(stacktrace.frames, [
    {
      platform: 'web:javascript',
      filename: result.asset,
      function: 'loadSubscription',
      in_app: true,
      lineno: 42,
      colno: 7,
      chunk_id: '11111111-2222-4333-8444-555555555555',
    },
  ]);
  assert.deepEqual(
    Object.keys(stacktrace.frames[0]).sort(),
    result.nativeFrameFields,
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
    assert.doesNotMatch(
      JSON.stringify(data),
      /private@example|private-identifier|synthetic-secret-token|\$current_url|\$referrer|\$device_id|\$session_id/,
    );
    assert.equal(data.properties.$geoip_disable, true);
  }
  return {
    scenario: options.live ? 'telemetry-sdk-live' : 'telemetry-sdk-wire',
    status: 'pass',
    ...(options.live ? { statuses: result.statuses } : {}),
  };
}
