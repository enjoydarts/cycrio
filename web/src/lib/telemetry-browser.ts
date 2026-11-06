import {
  createTelemetryController,
  type TelemetryConfiguration,
} from './telemetry.ts';
let active: ReturnType<typeof createTelemetryController> | undefined;

export function captureBrowserError(error: unknown) {
  active?.capture(error);
}

export function startBrowserTelemetry(apiBase: string) {
  active?.dispose();
  const controller = createTelemetryController({
    production: import.meta.env.PROD,
    target: window,
    origin: window.location.origin,
    getAssetPaths: () =>
      new Set(
        performance
          .getEntriesByType('resource')
          .filter(
            (entry) =>
              (entry as PerformanceResourceTiming).initiatorType === 'script',
          )
          .flatMap((entry) => {
            try {
              const url = new URL(entry.name);
              return url.origin === window.location.origin
                ? [url.pathname]
                : [];
            } catch {
              return [];
            }
          }),
      ),
    async getConfiguration(): Promise<TelemetryConfiguration> {
      const response = await fetch(
        `${apiBase.replace(/\/$/, '')}/v1/public/telemetry`,
        {
          credentials: 'omit',
          cache: 'no-store',
          signal: AbortSignal.timeout(5_000),
        },
      );
      if (!response.ok) throw new Error('Telemetry configuration unavailable');
      const body: unknown = await response.json();
      if (!body || typeof body !== 'object' || !('enabled' in body))
        throw new Error('Invalid telemetry configuration');
      if (body.enabled === false) return { enabled: false };
      if (
        body.enabled !== true ||
        !('project_token' in body) ||
        !('host' in body) ||
        typeof body.project_token !== 'string' ||
        typeof body.host !== 'string'
      )
        throw new Error('Invalid telemetry configuration');
      return {
        enabled: true,
        project_token: body.project_token,
        host: body.host,
      };
    },
    async loadSDK() {
      const { PostHog } = await import('posthog-js');
      return {
        initialize: (token, config) => {
          const instance = new PostHog().init(token, config);
          return {
            capture: (_event, properties) =>
              instance.captureException(
                new Error('Unhandled application error'),
                properties,
              ),
            reset: () => instance.reset(),
            // opt_out_capturing persists consent independently of persistence:'memory'.
            disable: () => instance.set_config({ before_send: () => null }),
          };
        },
      };
    },
  });
  active = controller;
  controller.start();
  return {
    setUser: controller.setUser,
    dispose() {
      controller.dispose();
      if (active === controller) active = undefined;
    },
  };
}
