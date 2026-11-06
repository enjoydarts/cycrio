import type { PostHogConfig } from 'posthog-js';

// This boundary exposes errors only. Analytics needs a separately reviewed policy.
export type TelemetryConfiguration =
  { enabled: false } | { enabled: true; project_token: string; host: string };
type Properties = Record<string, unknown>;
type SafeFrame = { filename: string; lineno: number; colno: number };
type SafeException = {
  type: 'Error';
  value: 'Unhandled application error';
  stacktrace?: { frames: SafeFrame[] };
};
export type TelemetrySDK = {
  capture: (event: string, properties: Properties) => unknown;
  reset: () => unknown;
  disable: () => unknown;
};
type SDKModule = {
  initialize: (token: string, options: Properties) => TelemetrySDK;
};
const uuidPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const assetPattern =
  /^\/_app\/immutable\/(?:chunks|entry|nodes)\/[A-Za-z0-9_.-]+\.js$/;
const anonymousIdentity = 'cycrio-browser-anonymous';
export function validPostHogConfiguration(
  projectToken: unknown,
  host: unknown,
): boolean {
  return (
    typeof projectToken === 'string' &&
    /^phc_[A-Za-z0-9_-]{8,}$/.test(projectToken) &&
    projectToken.length <= 1024 &&
    (host === 'https://us.i.posthog.com' || host === 'https://eu.i.posthog.com')
  );
}
export function sanitizeException(
  error: unknown,
  origin?: string,
  assetPaths?: ReadonlySet<string>,
): SafeException {
  const exception: SafeException = {
    type: 'Error',
    value: 'Unhandled application error',
  };
  try {
    if (!origin || !(error instanceof Error) || typeof error.stack !== 'string')
      return exception;
    const frames: SafeFrame[] = [];
    for (const line of error.stack.slice(0, 32_768).split('\n').slice(1, 31)) {
      const match = /(https?:\/\/[^\s)]+):(\d+):(\d+)\)?$/.exec(line);
      if (!match) continue;
      const url = new URL(match[1]);
      const lineno = Number(match[2]),
        colno = Number(match[3]);
      if (
        url.origin === origin &&
        !url.username &&
        !url.password &&
        assetPattern.test(url.pathname) &&
        assetPaths?.has(url.pathname) &&
        Number.isSafeInteger(lineno) &&
        lineno > 0 &&
        Number.isSafeInteger(colno) &&
        colno > 0
      )
        frames.push({ filename: url.pathname, lineno, colno });
    }
    if (frames.length) exception.stacktrace = { frames };
  } catch {
    /* Hostile Error subclasses must never break the application. */
  }
  return exception;
}
// Reconstruct, rather than redact, SDK events. No URLs, super-properties,
// person/device/session metadata, raw stack strings or custom fields survive.
export function filterTelemetryEvent(
  event: {
    event: string;
    properties: Properties;
  },
  assetPaths?: ReadonlySet<string>,
): { event: '$exception'; properties: Properties } | null {
  try {
    if (event.event !== '$exception') return null;
    const source = event.properties.$exception_list;
    if (!Array.isArray(source) || source.length !== 1) return null;
    const item = source[0];
    if (item?.type !== 'Error' || item?.value !== 'Unhandled application error')
      return null;
    const exception: SafeException = {
      type: 'Error',
      value: 'Unhandled application error',
    };
    const sourceFrames = item.stacktrace?.frames;
    if (Array.isArray(sourceFrames)) {
      const frames: SafeFrame[] = sourceFrames
        .slice(0, 30)
        .filter(
          (frame) =>
            typeof frame?.filename === 'string' &&
            assetPattern.test(frame.filename) &&
            assetPaths?.has(frame.filename) &&
            Number.isSafeInteger(frame.lineno) &&
            frame.lineno > 0 &&
            Number.isSafeInteger(frame.colno) &&
            frame.colno > 0,
        )
        .map((frame) => ({
          filename: frame.filename,
          lineno: frame.lineno,
          colno: frame.colno,
        }));
      if (frames.length) exception.stacktrace = { frames };
    }
    const properties: Properties = {
      $exception_list: [exception],
      $process_person_profile: false,
      $geoip_disable: true,
      runtime: 'browser',
      $lib: 'web',
      $lib_version: '1.447.2',
    };
    const distinctId = event.properties.distinct_id;
    if (
      typeof distinctId === 'string' &&
      (uuidPattern.test(distinctId) || distinctId === anonymousIdentity)
    )
      properties.distinct_id = distinctId;
    return { event: '$exception', properties };
  } catch {
    return null;
  }
}
export function createTelemetryController(options: {
  production: boolean;
  target: Pick<EventTarget, 'addEventListener' | 'removeEventListener'>;
  origin?: string;
  getAssetPaths?: () => ReadonlySet<string>;
  getConfiguration: () => Promise<TelemetryConfiguration>;
  loadSDK: () => Promise<SDKModule>;
  refreshMilliseconds?: number;
}) {
  let sdk: TelemetrySDK | undefined;
  let configurationKey = '';
  let user: string | null = null;
  let generation = 0;
  let disposed = false;
  let started = false;
  let timer: ReturnType<typeof setInterval> | undefined;
  const seen = new WeakSet<Error>();
  const safely = (operation: () => unknown) => {
    try {
      operation();
    } catch {
      /* Telemetry is optional. */
    }
  };
  function stop() {
    if (sdk) {
      const old = sdk;
      sdk = undefined;
      safely(() => old.disable());
      safely(() => old.reset());
    }
    configurationKey = '';
  }
  async function refresh() {
    if (!options.production || disposed || !started) return;
    const current = ++generation;
    try {
      const config = await options.getConfiguration();
      if (disposed || generation !== current) return;
      if (
        !config.enabled ||
        !validPostHogConfiguration(config.project_token, config.host)
      ) {
        stop();
        return;
      }
      const key = `${config.host}:${config.project_token}`;
      if (sdk && key === configurationKey) return;
      stop();
      const module = await options.loadSDK();
      if (disposed || generation !== current) return;
      sdk = module.initialize(config.project_token, {
        api_host: config.host,
        autocapture: false,
        capture_pageview: false,
        capture_pageleave: false,
        capture_exceptions: false,
        capture_performance: false,
        capture_heatmaps: false,
        capture_dead_clicks: false,
        rageclick: false,
        disable_session_recording: true,
        disable_surveys: true,
        disable_surveys_automatic_display: true,
        disable_web_experiments: true,
        disable_product_tours: true,
        disable_conversations: true,
        disableDeviceModel: true,
        capture_webmcp: false,
        disable_external_dependency_loading: true,
        advanced_disable_flags: true,
        advanced_disable_feature_flags: true,
        advanced_disable_feature_flags_on_first_load: true,
        advanced_disable_toolbar_metrics: true,
        enable_recording_console_log: false,
        persistence: 'memory',
        disable_persistence: true,
        person_profiles: 'never',
        ip: false,
        save_referrer: false,
        save_campaign_params: false,
        request_batching: false,
        logs: { captureConsoleLogs: false },
        metrics: { network: false, beforeSend: () => null },
        debug: false,
        bootstrap: { distinctID: anonymousIdentity, isIdentifiedID: false },
        on_request_error: () => {},
        before_send: (event: {
          event: string;
          properties: Properties;
          uuid: string;
          timestamp?: Date;
        }) => {
          if (disposed || !sdk || configurationKey !== key) return null;
          const filtered = filterTelemetryEvent(
            event,
            options.getAssetPaths?.(),
          );
          if (!filtered) return null;
          filtered.properties.distinct_id = user ?? anonymousIdentity;
          filtered.properties.token = config.project_token;
          return {
            ...filtered,
            uuid: event.uuid,
            ...(event.timestamp instanceof Date
              ? { timestamp: event.timestamp }
              : {}),
          };
        },
      } satisfies Partial<PostHogConfig>);
      configurationKey = key;
    } catch {
      if (!disposed && generation === current) stop();
    }
  }
  function capture(error: unknown) {
    if (disposed || !sdk) return;
    try {
      if (error instanceof Error) {
        if (seen.has(error)) return;
        seen.add(error);
      }
      const properties: Properties = {
        $exception_list: [
          sanitizeException(error, options.origin, options.getAssetPaths?.()),
        ],
        distinct_id: user ?? anonymousIdentity,
      };
      safely(() => sdk?.capture('$exception', properties));
    } catch {
      /* Window errors and arbitrary rejection values are untrusted. */
    }
  }
  const windowError = (event: Event) => capture((event as ErrorEvent).error);
  const rejection = (event: Event) =>
    capture((event as PromiseRejectionEvent).reason);
  return {
    start() {
      if (started || disposed || !options.production) return;
      started = true;
      options.target.addEventListener('error', windowError);
      options.target.addEventListener('unhandledrejection', rejection);
      timer = setInterval(() => {
        void refresh();
      }, options.refreshMilliseconds ?? 60_000);
      void refresh();
    },
    refresh,
    setUser(id: string | null) {
      const next = typeof id === 'string' && uuidPattern.test(id) ? id : null;
      if (next === user) return;
      if (sdk) safely(() => sdk?.reset());
      user = next;
    },
    capture,
    dispose() {
      disposed = true;
      generation++;
      user = null;
      clearInterval(timer);
      options.target.removeEventListener('error', windowError);
      options.target.removeEventListener('unhandledrejection', rejection);
      stop();
    },
  };
}
