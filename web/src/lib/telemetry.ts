import type { PostHogConfig } from 'posthog-js';
import type { ErrorTracking } from '@posthog/core';
import { createDefaultStackParser } from '@posthog/core/error-tracking';
import { scrubText } from './telemetry-redaction.ts';
import {
  pageForPath,
  pageEventProperties,
  type TelemetryPage,
} from './telemetry-pages.ts';
export { scrubText } from './telemetry-redaction.ts';

// Exception diagnostics and explicit page events use separate allowlists.
export type TelemetryConfiguration =
  { enabled: false } | { enabled: true; project_token: string; host: string };
type Properties = Record<string, unknown>;
type SafeFrame = Required<
  Pick<
    ErrorTracking.StackFrame,
    'platform' | 'filename' | 'function' | 'in_app' | 'lineno' | 'colno'
  >
> & {
  platform: 'web:javascript';
  in_app: true;
  chunk_id?: string;
};
type SafeException = {
  type: string;
  value: string;
  mechanism?: ErrorTracking.Mechanism;
  stacktrace?: NonNullable<ErrorTracking.Exception['stacktrace']> & {
    frames: SafeFrame[];
  };
};
export type TelemetrySDK = {
  captureException: (error: Error, properties: Properties) => unknown;
  capturePage?: (
    event: '$pageview' | '$pageleave',
    properties: Properties,
  ) => unknown;
  getSessionId?: () => string;
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
const parseStack = createDefaultStackParser();
function exceptionType(value: unknown): string {
  if (typeof value !== 'string' || !/^[$A-Z_a-z][$\w.-]{0,127}$/.test(value))
    return 'Error';
  return scrubText(value) === value ? value : 'Error';
}
function assetURL(
  value: unknown,
  origin?: string,
  assets?: ReadonlySet<string>,
): string | null {
  if (typeof value !== 'string' || !assets || !origin) return null;
  try {
    const url = new URL(value, origin);
    if (
      url.origin !== origin ||
      url.username ||
      url.password ||
      !assetPattern.test(url.pathname)
    )
      return null;
    const normalized = url.origin + url.pathname;
    return assets.has(normalized) || assets.has(url.pathname)
      ? normalized
      : null;
  } catch {
    return null;
  }
}
function safeFrame(
  source: ErrorTracking.StackFrame,
  origin?: string,
  assets?: ReadonlySet<string>,
): SafeFrame | null {
  const filename = assetURL(source.filename, origin, assets);
  if (
    !filename ||
    !Number.isSafeInteger(source.lineno) ||
    source.lineno! <= 0 ||
    !Number.isSafeInteger(source.colno) ||
    source.colno! <= 0
  )
    return null;
  const frame: SafeFrame = {
    platform: 'web:javascript',
    filename,
    function:
      typeof source.function === 'string'
        ? scrubText(source.function)
            .replace(/https?:\/\/\S+/g, '[redacted]')
            .replace(/[\r\n]+/g, ' ')
            .slice(0, 256) || '?'
        : '?',
    in_app: true,
    lineno: source.lineno!,
    colno: source.colno!,
  };
  if (typeof source.chunk_id === 'string' && uuidPattern.test(source.chunk_id))
    frame.chunk_id = source.chunk_id;
  return frame;
}
function safeMechanism(source: unknown): ErrorTracking.Mechanism | undefined {
  if (!source || typeof source !== 'object') return undefined;
  const values = source as Record<string, unknown>;
  const result: ErrorTracking.Mechanism = {};
  for (const key of ['type', 'source'] as const)
    if (typeof values[key] === 'string')
      result[key] = scrubText(values[key]).slice(0, 128);
  for (const key of ['handled', 'synthetic'] as const)
    if (typeof values[key] === 'boolean') result[key] = values[key];
  for (const key of ['exception_id', 'parent_id'] as const)
    if (Number.isSafeInteger(values[key]) && (values[key] as number) >= 0)
      result[key] = values[key] as number;
  return Object.keys(result).length ? result : undefined;
}
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
    value:
      typeof error === 'string'
        ? scrubText(error)
        : 'Unhandled non-Error rejection',
  };
  try {
    let originalName = 'Error',
      originalMessage = '',
      rawStack: unknown;
    if (
      error instanceof Error ||
      (typeof DOMException !== 'undefined' && error instanceof DOMException)
    ) {
      originalName = typeof error.name === 'string' ? error.name : 'Error';
      originalMessage = typeof error.message === 'string' ? error.message : '';
      rawStack = 'stack' in error ? error.stack : undefined;
    } else if (error !== null && typeof error === 'object') {
      // SDK and cross-realm rejections may not inherit this realm's Error.
      // Read only own data fields; never invoke getters or serialize payloads.
      const field = (key: string) => {
        const value = Object.getOwnPropertyDescriptor(error, key)?.value;
        return typeof value === 'string' ? value : undefined;
      };
      const message = field('message');
      if (message === undefined) return exception;
      originalMessage = message;
      originalName = field('name') ?? 'Error';
      rawStack = field('stack');
    } else return exception;
    exception.type = exceptionType(originalName);
    exception.value = scrubText(originalMessage);
    if (!origin || typeof rawStack !== 'string') return exception;
    let stack = rawStack.slice(0, 65_536);
    const header = `${originalName}${originalMessage ? ': ' + originalMessage : ''}`;
    // V8 includes the entire multiline message before the real stack. Remove
    // that exact prefix, so a message cannot inject an allowlisted fake frame.
    if (stack.startsWith(header)) stack = stack.slice(header.length);
    else if (originalMessage && stack.startsWith(originalMessage))
      stack = stack.slice(originalMessage.length);
    else if (stack.startsWith(originalName + ':')) return exception;
    const frames = parseStack(stack)
      .flatMap((frame) => {
        const safe = safeFrame(frame, origin, assetPaths);
        return safe ? [safe] : [];
      })
      .slice(0, 30);
    if (frames.length) exception.stacktrace = { type: 'raw', frames };
  } catch {
    /* Hostile Error subclasses must never break the application. */
  }
  return exception;
}
export function sanitizeError(
  error: unknown,
  origin?: string,
  assets?: ReadonlySet<string>,
): Error {
  return safeErrorTree(error, origin, assets, new WeakSet(), 0, {
    remaining: 16,
  });
}
function safeErrorTree(
  error: unknown,
  origin: string | undefined,
  assets: ReadonlySet<string> | undefined,
  seen: WeakSet<object>,
  depth: number,
  budget: { remaining: number },
): Error {
  budget.remaining--;
  const exception = sanitizeException(error, origin, assets);
  let children: Error[] | undefined;
  let cause: Error | undefined;
  try {
    if (error instanceof Error && !seen.has(error) && depth < 4) {
      seen.add(error);
      if (
        error.cause !== undefined &&
        budget.remaining > 0 &&
        !(
          typeof error.cause === 'object' &&
          error.cause !== null &&
          seen.has(error.cause)
        )
      )
        cause = safeErrorTree(
          error.cause,
          origin,
          assets,
          seen,
          depth + 1,
          budget,
        );
      if (error instanceof AggregateError && Array.isArray(error.errors)) {
        children = [];
        for (const child of error.errors.slice(0, 4)) {
          if (budget.remaining <= 0) break;
          children.push(
            safeErrorTree(child, origin, assets, seen, depth + 1, budget),
          );
        }
      }
    }
  } catch {
    /* Do not inspect arbitrary cause/rejection object fields or throwing getters. */
  }
  const safe = children
    ? new AggregateError(children, exception.value)
    : new Error(exception.value);
  if (cause) safe.cause = cause;
  safe.name = exception.type;
  const lines =
    exception.stacktrace?.frames
      .slice()
      .reverse()
      .map(
        (frame) =>
          `    at ${frame.function} (${frame.filename}:${frame.lineno}:${frame.colno})`,
      ) ?? [];
  // The SDK reparses stack text. Multiline messages belong only in .message,
  // otherwise a message's frame-looking line could become a real SDK frame.
  safe.stack = `${safe.name}${lines.length ? '\n' + lines.join('\n') : ''}`;
  return safe;
}
// Keep native exception diagnostics and code-asset URLs. Request payloads,
// person/device/session metadata and unrelated custom fields are never copied.
export function filterTelemetryEvent(
  event: {
    event: string;
    properties: Properties;
  },
  assetPaths?: ReadonlySet<string>,
  origin?: string,
): { event: '$exception'; properties: Properties } | null {
  try {
    if (event.event !== '$exception') return null;
    const source = event.properties.$exception_list;
    if (!Array.isArray(source) || !source.length) return null;
    const exceptions: SafeException[] = [];
    for (const item of source.slice(0, 16)) {
      if (
        !item ||
        typeof item.type !== 'string' ||
        typeof item.value !== 'string'
      )
        return null;
      const exception: SafeException = {
        type: exceptionType(item.type),
        value: scrubText(item.value),
      };
      const mechanism = safeMechanism(item.mechanism);
      if (mechanism) exception.mechanism = mechanism;
      const sourceFrames = item.stacktrace?.frames;
      if (Array.isArray(sourceFrames)) {
        const frames: SafeFrame[] = sourceFrames
          .slice(0, 30)
          .flatMap((frame) => {
            if (!frame || typeof frame !== 'object') return [];
            const safe = safeFrame(frame, origin, assetPaths);
            return safe ? [safe] : [];
          });
        if (frames.length) exception.stacktrace = { type: 'raw', frames };
      }
      exceptions.push(exception);
    }
    const properties: Properties = {
      $exception_list: exceptions,
      $process_person_profile: false,
      $geoip_disable: true,
      runtime: 'browser',
      $lib: 'web',
      $lib_version: '1.447.2',
    };
    for (const key of [
      '$browser',
      '$browser_version',
      '$os',
      '$os_version',
      '$device_type',
    ] as const) {
      const value = event.properties[key];
      if (typeof value === 'string')
        properties[key] = scrubText(value).slice(0, 128);
      else if (typeof value === 'number' && Number.isFinite(value))
        properties[key] = value;
    }
    if (
      ['fatal', 'error', 'warning', 'log', 'info', 'debug'].includes(
        String(event.properties.$exception_level),
      )
    )
      properties.$exception_level = event.properties.$exception_level;
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
  now?: () => number;
  newID?: () => string;
}) {
  let sdk: TelemetrySDK | undefined;
  let configurationKey = '';
  let user: string | null = null;
  let generation = 0;
  let disposed = false;
  let started = false;
  let timer: ReturnType<typeof setInterval> | undefined;
  const seen = new WeakSet<object>();
  type PageView = {
    page: TelemetryPage;
    sessionID: string;
    id: string;
    owner: string;
    startedAt: number;
  };
  let requestedPath: string | null = null;
  let requestedPage: TelemetryPage | null = null;
  let pageVisible = true;
  let currentPage: PageView | undefined;
  let anonymousPageIdentity: string | undefined;
  let pendingPageEvent:
    { event: '$pageview' | '$pageleave'; properties: Properties } | undefined;
  const now = options.now ?? Date.now;
  const newID = options.newID ?? (() => crypto.randomUUID());
  const safely = (operation: () => unknown) => {
    try {
      operation();
    } catch {
      /* Telemetry is optional. */
    }
  };
  function stop() {
    currentPage = undefined;
    pendingPageEvent = undefined;
    if (sdk) {
      const old = sdk;
      sdk = undefined;
      safely(() => old.disable());
      safely(() => old.reset());
    }
    configurationKey = '';
  }
  function sendPage(view: PageView, duration?: number) {
    if (!sdk?.capturePage || disposed || !options.origin) return false;
    const properties = pageEventProperties(
      view.page,
      options.origin,
      view.sessionID,
      view.id,
      duration,
    );
    if (!properties) return false;
    properties.distinct_id = view.owner;
    const event = duration === undefined ? '$pageview' : '$pageleave';
    // The SDK adds URL/title/referrer/device metadata. For page events, the
    // final hook uses only this synchronous, application-owned snapshot.
    // Unsolicited SDK page events remain blocked.
    pendingPageEvent = { event, properties };
    try {
      sdk.capturePage(event, { ...properties });
      return true;
    } catch {
      /* Page measurement never affects navigation. */
      return false;
    } finally {
      pendingPageEvent = undefined;
    }
  }
  function closePage() {
    const view = currentPage;
    currentPage = undefined;
    if (!view) return;
    const duration = Math.min(
      86400,
      Math.max(0, (now() - view.startedAt) / 1000),
    );
    sendPage(view, duration);
  }
  function openPage() {
    if (
      !started ||
      disposed ||
      !pageVisible ||
      currentPage ||
      !requestedPage ||
      !sdk?.capturePage ||
      !sdk.getSessionId ||
      !options.origin ||
      (!user && requestedPage.path !== '/')
    )
      return;
    try {
      const sessionID = sdk.getSessionId();
      const id = newID();
      if (!pageEventProperties(requestedPage, options.origin, sessionID, id))
        return;
      if (!user && !anonymousPageIdentity) {
        const anonymousID = newID();
        if (!uuidPattern.test(anonymousID)) return;
        anonymousPageIdentity = `cycrio-visitor-${anonymousID}`;
      }
      const view = {
        page: requestedPage,
        sessionID,
        id,
        owner: user ?? anonymousPageIdentity!,
        startedAt: now(),
      };
      if (sendPage(view)) currentPage = view;
    } catch {
      /* Invalid or unavailable SDK state disables page measurement only. */
    }
  }
  function navigate(pathname: string) {
    if (disposed) return;
    const page = pageForPath(pathname);
    if (page && requestedPath === pathname) {
      openPage();
      return;
    }
    closePage();
    requestedPath = page ? pathname : null;
    requestedPage = page;
    openPage();
  }
  function leavePage() {
    pageVisible = false;
    closePage();
  }
  function resumePage() {
    pageVisible = true;
    openPage();
  }
  const pageHidden = () => leavePage();
  const pageShown = (event: Event) => {
    if ((event as PageTransitionEvent).persisted) resumePage();
  };
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
      if (sdk && key === configurationKey) {
        openPage();
        return;
      }
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
          if (event.event === '$pageview' || event.event === '$pageleave') {
            if (!pendingPageEvent || pendingPageEvent.event !== event.event)
              return null;
            return {
              event: pendingPageEvent.event,
              properties: {
                ...pendingPageEvent.properties,
                token: config.project_token,
                $lib: 'web',
                $lib_version: '1.447.2',
              },
              uuid: event.uuid,
              ...(event.timestamp instanceof Date
                ? { timestamp: event.timestamp }
                : {}),
            };
          }
          const filtered = filterTelemetryEvent(
            event,
            options.getAssetPaths?.(),
            options.origin,
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
      openPage();
    } catch {
      if (!disposed && generation === current) stop();
    }
  }
  function capture(error: unknown) {
    if (disposed || !sdk) return;
    try {
      if (error !== null && typeof error === 'object') {
        if (seen.has(error)) return;
        seen.add(error);
      }
      const properties: Properties = {
        distinct_id: user ?? anonymousIdentity,
      };
      const safe = sanitizeError(
        error,
        options.origin,
        options.getAssetPaths?.(),
      );
      safely(() => sdk?.captureException(safe, properties));
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
      options.target.addEventListener('pagehide', pageHidden);
      options.target.addEventListener('pageshow', pageShown);
      timer = setInterval(() => {
        void refresh();
      }, options.refreshMilliseconds ?? 60_000);
      void refresh();
    },
    refresh,
    navigate,
    leavePage,
    resumePage,
    setUser(id: string | null) {
      const next = typeof id === 'string' && uuidPattern.test(id) ? id : null;
      if (next === user) return;
      closePage();
      if (sdk) safely(() => sdk?.reset());
      user = next;
      anonymousPageIdentity = undefined;
      openPage();
    },
    capture,
    dispose() {
      closePage();
      disposed = true;
      generation++;
      user = null;
      clearInterval(timer);
      options.target.removeEventListener('error', windowError);
      options.target.removeEventListener('unhandledrejection', rejection);
      options.target.removeEventListener('pagehide', pageHidden);
      options.target.removeEventListener('pageshow', pageShown);
      stop();
    },
  };
}
