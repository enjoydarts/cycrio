import type { RouteProof } from './ai-routes-api.ts';
type DisplayRoute = Pick<RouteProof, 'provider_name' | 'tag' | 'quantization'>;
export function routeQuantization(
  route: Pick<RouteProof, 'quantization'>,
): string | null {
  const value = route.quantization.trim();
  return value && value.toLowerCase() !== 'unknown' ? value : null;
}
export function routeDisplayName(route: DisplayRoute): string {
  const precision = routeQuantization(route);
  const [provider, ...variant] = route.tag.split('/');
  const suffix = variant.join('/');
  const name =
    provider === 'google-vertex' ? 'Google Vertex AI' : route.provider_name;
  const regions: Record<string, string> = {
    europe: '欧州',
    global: 'グローバル',
    us: '米国',
  };
  const detail =
    provider === 'google-vertex' ? (regions[suffix] ?? suffix) : suffix;
  return [
    name,
    detail,
    precision && precision.toLowerCase() !== suffix.toLowerCase()
      ? precision
      : null,
  ]
    .filter(Boolean)
    .join(' · ');
}
