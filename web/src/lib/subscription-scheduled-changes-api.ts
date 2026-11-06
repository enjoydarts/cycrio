import type { TokenProvider } from './api.ts';
import {
  array,
  choice,
  createDailyRequest,
  date,
  input,
  integer,
  invalid,
  nullable,
  pagination,
  record,
  timestamp,
  uuid,
} from './daily-api.ts';
import { parseAdditionalSources } from './candidate-selection.ts';
import {
  changeFields,
  parseChangePreview,
  type ChangeInput,
  type ChangeKey,
  type ChangePreview,
  type ChangePreviewInput,
} from './subscription-changes-api.ts';
import { parseSubscription, type Subscription } from './subscriptions-api.ts';

export type ScheduledPreview = ChangePreview & { scheduled_for: string };
export type ScheduledInput = ChangeInput & { effective_on: string };
export type ScheduledChange = {
  id: string;
  request_id: string;
  subscription_id: string;
  version: number;
  state: 'pending' | 'applied' | 'cancelled' | 'needs_review';
  effective_on: string;
  base_version: number;
  preview: ScheduledPreview;
  selected_fields: ChangeKey[];
  created_at: string;
  applied_at: string | null;
  reason:
    | ''
    | 'subscription_changed'
    | 'subscription_deleted'
    | 'source_changed'
    | 'algorithm_changed'
    | 'request_conflict'
    | 'invalid_values';
};
export type ScheduledChangeResult = {
  schedule: ScheduledChange;
  current: Subscription | null;
};
export type ScheduledChangePage = {
  schedules: ScheduledChange[];
  total: number;
  limit: number;
  offset: number;
};
const keys = (value: unknown): ChangeKey[] => {
  const result = array(
    value,
    (v) => choice(v, changeFields),
    changeFields.length,
  );
  return result.length && new Set(result).size === result.length
    ? result
    : invalid();
};
const tokenValue = (value: unknown) =>
  typeof value === 'string' && /^[a-f0-9]{64}$/.test(value) ? value : invalid();
export function parseScheduledPreview(value: unknown): ScheduledPreview {
  const raw = record(value),
    preview = parseChangePreview(raw),
    scheduled_for = date(raw.scheduled_for);
  return scheduled_for > preview.as_of &&
    scheduled_for === preview.source.original_event.effective_date
    ? { ...preview, scheduled_for }
    : invalid();
}
export function parseScheduledChange(value: unknown): ScheduledChange {
  const raw = record(value);
  const result: ScheduledChange = {
    id: uuid(raw.id),
    request_id: uuid(raw.request_id),
    subscription_id: uuid(raw.subscription_id),
    version: integer(raw.version, 1),
    state: choice(raw.state, [
      'pending',
      'applied',
      'cancelled',
      'needs_review',
    ]),
    effective_on: date(raw.effective_on),
    base_version: integer(raw.base_version, 1),
    preview: parseScheduledPreview(raw.preview),
    selected_fields: keys(raw.selected_fields),
    created_at: timestamp(raw.created_at),
    applied_at: nullable(raw.applied_at, timestamp),
    reason: choice(raw.reason, [
      '',
      'subscription_changed',
      'subscription_deleted',
      'source_changed',
      'algorithm_changed',
      'request_conflict',
      'invalid_values',
    ]),
  };
  return result.preview.subscription.id === result.subscription_id &&
    result.preview.subscription.version === result.base_version &&
    result.effective_on === result.preview.scheduled_for &&
    result.selected_fields.every((key) =>
      result.preview.fields.some(
        (field) =>
          field.key === key &&
          field.selectable &&
          field.reason === '' &&
          field.proposed !== null &&
          JSON.stringify(field.current) !== JSON.stringify(field.proposed),
      ),
    ) &&
    (result.state === 'applied') === (result.applied_at !== null)
    ? result
    : invalid();
}
export function createScheduledChangesAPI(
  base: string,
  fetcher: typeof fetch = fetch,
) {
  const request = createDailyRequest(base, fetcher);
  const path = (id: string) => `/v1/subscriptions/${input(() => uuid(id))}`;
  const source = (v: ChangePreviewInput) => ({
    expected_version: integer(v.expected_version, 1),
    source_result_id: uuid(v.source_result_id),
    source_event_index: integer(v.source_event_index, 0, 31),
    ...parseAdditionalSources(
      { result_id: v.source_result_id, event_index: v.source_event_index },
      v.additional_sources,
    ),
  });
  const matches = (p: ChangePreview, id: string, v: ChangePreviewInput) =>
    p.subscription.id === id &&
    p.subscription.version === v.expected_version &&
    p.source.result_id === v.source_result_id &&
    p.source.event_index === v.source_event_index &&
    JSON.stringify(
      p.additional_evidence
        .map((e) => `${e.result_id}/${e.event_index}`)
        .sort(),
    ) ===
      JSON.stringify(
        (v.additional_sources ?? [])
          .map((e) => `${e.result_id}/${e.event_index}`)
          .sort(),
      );
  return {
    async preview(
      id: string,
      v: ChangePreviewInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<ScheduledPreview> {
      const result = parseScheduledPreview(
        await request(
          `${path(id)}/scheduled-change-preview`,
          token,
          signal,
          'POST',
          input(() => source(v)),
        ),
      );
      return matches(result, id, v) ? result : invalid();
    },
    async apply(
      id: string,
      v: ScheduledInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<ScheduledChangeResult> {
      const body = input(() => {
        if (v.confirmed !== true) return invalid();
        return {
          ...source(v),
          request_id: uuid(v.request_id),
          preview_token: tokenValue(v.preview_token),
          selected_fields: keys(v.selected_fields),
          confirmed: true,
          effective_on: date(v.effective_on),
        };
      });
      const raw = record(
        await request(
          `${path(id)}/scheduled-changes`,
          token,
          signal,
          'POST',
          body,
        ),
      );
      const schedule = parseScheduledChange(raw.schedule),
        current = nullable(raw.current, parseSubscription);
      return schedule.request_id === v.request_id &&
        matches(schedule.preview, id, v) &&
        schedule.effective_on === v.effective_on &&
        schedule.preview.preview_token === v.preview_token &&
        JSON.stringify([...schedule.selected_fields].sort()) ===
          JSON.stringify([...v.selected_fields].sort()) &&
        (!current ||
          (current.id === id && current.version >= schedule.base_version))
        ? { schedule, current }
        : invalid();
    },
    async list(
      id: string,
      offset: number,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<ScheduledChangePage> {
      const query = input(
        () =>
          new URLSearchParams({ limit: '20', offset: String(integer(offset)) }),
      );
      const raw = record(
        await request(`${path(id)}/scheduled-changes?${query}`, token, signal),
      );
      const schedules = array(raw.schedules, parseScheduledChange);
      return schedules.every((schedule) => schedule.subscription_id === id)
        ? { schedules, ...pagination(raw) }
        : invalid();
    },
    async cancel(
      id: string,
      scheduleID: string,
      version: number,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<ScheduledChange> {
      const route = `${path(id)}/scheduled-changes/${input(() => uuid(scheduleID))}/cancel`;
      const result = parseScheduledChange(
        await request(
          route,
          token,
          signal,
          'POST',
          input(() => ({
            expected_version: integer(version, 1),
            confirmed: true,
          })),
        ),
      );
      return result.subscription_id === id &&
        result.id === scheduleID &&
        result.state === 'cancelled' &&
        result.version >= version
        ? result
        : invalid();
    },
  };
}
export type ScheduledChangesAPI = ReturnType<typeof createScheduledChangesAPI>;
