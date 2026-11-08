import { settingsSections } from './settings.ts';

export type TelemetryPage = { name: string; path: string };

const staticPages = new Map<string, string>([
  ['/', 'home'],
  ['/mail', 'mail'],
  ['/import', 'import'],
  ['/subscriptions', 'subscriptions'],
  ['/analysis', 'analysis'],
  ['/reports', 'reports'],
  ['/admin', 'admin'],
  ['/settings', 'settings'],
]);
const detailPage: TelemetryPage = {
  name: 'subscription_detail',
  path: '/subscriptions/detail',
};

function validUUID(value: string): boolean {
  return (
    value.length === 36 &&
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
      value,
    )
  );
}

export function pageForPath(pathname: string): TelemetryPage | null {
  if (/[?#%\\\s]/.test(pathname)) return null;
  const path =
    pathname !== '/' && pathname.endsWith('/')
      ? pathname.slice(0, -1)
      : pathname;
  const name = staticPages.get(path);
  if (name) return { name, path };

  const detailPrefix = '/subscriptions/';
  if (
    path.startsWith(detailPrefix) &&
    validUUID(path.slice(detailPrefix.length))
  ) {
    return { ...detailPage };
  }

  const section = settingsSections.find(
    (item) => path === `/settings/${item.id}`,
  );
  if (!section) return null;
  const sectionID = 'aliasFor' in section ? section.aliasFor : section.id;
  return { name: `settings_${sectionID}`, path: `/settings/${sectionID}` };
}

export function pageEventProperties(
  page: TelemetryPage,
  origin: string,
  sessionID: string,
  pageviewID: string,
  durationSeconds?: number,
): Record<string, unknown> | null {
  const canonical =
    page.path === detailPage.path ? detailPage : pageForPath(page.path);
  if (
    !canonical ||
    canonical.name !== page.name ||
    canonical.path !== page.path
  ) {
    return null;
  }
  if (!validUUID(sessionID) || !validUUID(pageviewID)) return null;
  if (
    durationSeconds !== undefined &&
    (!Number.isFinite(durationSeconds) ||
      durationSeconds < 0 ||
      durationSeconds > 86400)
  )
    return null;

  let url: URL;
  try {
    url = new URL(origin);
  } catch {
    return null;
  }
  if (
    !['http:', 'https:'].includes(url.protocol) ||
    url.origin !== origin ||
    url.username ||
    url.password
  )
    return null;

  const properties: Record<string, unknown> = {
    $current_url: origin + canonical.path,
    $host: url.host,
    $pathname: canonical.path,
    page_name: canonical.name,
    $session_id: sessionID,
    $pageview_id: pageviewID,
    $process_person_profile: false,
    $geoip_disable: true,
    runtime: 'browser',
  };
  if (durationSeconds !== undefined) {
    properties.$prev_pageview_duration = durationSeconds;
    properties.$prev_pageview_pathname = canonical.path;
    properties.$prev_pageview_id = pageviewID;
    properties.$prev_pageview_host = url.host;
  }
  return properties;
}
