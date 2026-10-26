import type { TokenProvider } from './api.ts';
import type { AnalysisAPI } from './analysis-api.ts';
import { createFeed, type FeedView } from './daily-controller.ts';
export function createAnalysisController(
  api: AnalysisAPI,
  token: TokenProvider,
  changed: (v: FeedView<Awaited<ReturnType<AnalysisAPI['results']>>>) => void,
  onDenied: () => void,
) {
  return createFeed(api.results, token, changed, onDenied);
}
export function createAnalysisHistory(
  api: AnalysisAPI,
  token: TokenProvider,
  changed: (v: FeedView<Awaited<ReturnType<AnalysisAPI['runs']>>>) => void,
  onDenied: () => void,
) {
  return createFeed(api.runs, token, changed, onDenied);
}
