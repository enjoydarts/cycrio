import { nullable, text } from './daily-api.ts';
export type SubscriptionName = { display_name?: string | null };
export const subscriptionTitle = (
  value: SubscriptionName & { service: string },
): string => value.display_name?.trim() || value.service;
export const parseDisplayName = (value: unknown): SubscriptionName =>
  value === undefined
    ? {}
    : { display_name: nullable(value, (x) => text(x, 200).trim()) };
export const normalizeDisplayName = (value: string | null): string | null =>
  value === null || value.trim() === '' ? null : text(value.trim(), 200);
