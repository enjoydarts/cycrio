import type { HandleClientError } from '@sveltejs/kit/hooks';
import { updated } from '$app/state';
import { captureBrowserError } from '#lib/telemetry-browser.ts';

export const handleError: HandleClientError = async ({ error, kind }) => {
  if (kind !== 'unknown') return;

  if (isLocalRouteModuleImportError(error)) {
    try {
      // SvelteKit already checks the version and performs a document navigation
      // on failed route imports. Hover preloads reach this hook before that
      // check; avoid reporting their recoverable deployment skew as app bugs.
      if (await updated.check()) return;
    } catch {
      // A failed version check is not evidence of a recoverable old release.
    }
  }
  captureBrowserError(error);
};

function isLocalRouteModuleImportError(error: unknown): boolean {
  if (!(error instanceof TypeError)) return false;
  const match =
    /^(?:error loading dynamically imported module|Failed to fetch dynamically imported module): (https?:\/\/\S+)$/.exec(
      error.message,
    );
  if (!match) return false;
  try {
    const url = new URL(match[1]);
    return (
      url.origin === window.location.origin &&
      /^\/_app\/immutable\/(nodes|chunks)\/[\w.-]+\.js$/.test(url.pathname) &&
      !url.search &&
      !url.hash &&
      !url.username &&
      !url.password
    );
  } catch {
    return false;
  }
}
