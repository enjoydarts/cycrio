import type { HandleClientError } from '@sveltejs/kit/hooks';
import { captureBrowserError } from '#lib/telemetry-browser.ts';

export const handleError: HandleClientError = ({ error, kind }) => {
  if (kind === 'unknown') captureBrowserError(error);
};
