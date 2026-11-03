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
  text,
  timestamp,
  uuid,
} from './daily-api.ts';
import {
  parseProcessingEvent,
  type ProcessingEvent,
} from './mail-processing-api.ts';
import {
  parseAdditionalSources,
  type CandidateSource,
} from './candidate-selection.ts';
import { parseSubscription, type Subscription } from './subscriptions-api.ts';

export const changeFields = [
  'trial_end_date',
  'auto_renew',
  'amount',
  'currency',
  'interval',
  'plan',
  'contract_kind',
  'effective_date',
  'access_end_date',
  'next_billing_date',
] as const;
export type ChangeKey = (typeof changeFields)[number];
export type ChangeValue =
  | boolean
  | string
  | null
  | { unit: 'day' | 'week' | 'month' | 'year'; count: number };
export type ChangeField = {
  key: ChangeKey;
  current: ChangeValue;
  proposed: ChangeValue;
  selectable: boolean;
  reason: string;
};
export type ChangeEvidence = {
  result_id: string;
  event_index: number;
  received_at: string | null;
  original_event: ProcessingEvent;
};
export type ChangeSource = {
  source_result_id: string;
  source_event_index: number;
  additional_sources?: CandidateSource[];
};
export type ChangePreviewInput = ChangeSource & { expected_version: number };
export type ChangeInput = ChangePreviewInput & {
  request_id: string;
  preview_token: string;
  selected_fields: ChangeKey[];
  confirmed: boolean;
};
export type ChangePreview = {
  subscription: Subscription;
  source: ChangeEvidence;
  additional_evidence: ChangeEvidence[];
  preview_token: string;
  as_of: string;
  timezone: 'Asia/Tokyo';
  algorithm_version: string;
  fields: ChangeField[];
  warnings: { code: string; field?: ChangeKey }[];
  derived_next_billing_date: string | null;
};
export type ChangeReceipt = {
  request_id: string;
  applied_version: number;
  created_at: string;
  selected_fields: ChangeKey[];
  before: Subscription;
  after: Subscription;
  source: ChangeEvidence;
  additional_evidence: ChangeEvidence[];
  algorithm_version: string;
};
export type ChangeResult = {
  receipt: ChangeReceipt;
  current: Subscription | null;
};
export type ChangePage = {
  changes: ChangeReceipt[];
  total: number;
  limit: number;
  offset: number;
};
const key = (v: unknown) => choice(v, changeFields);
const keys = (v: unknown) => {
  const result = array(v, key, changeFields.length);
  return new Set(result).size === result.length ? result : invalid();
};
const tokenValue = (v: unknown) =>
  typeof v === 'string' && /^[a-f0-9]{64}$/.test(v) ? v : invalid();
const evidence = (value: unknown): ChangeEvidence => {
  const v = record(value);
  return {
    result_id: uuid(v.result_id),
    event_index: integer(v.event_index, 0, 31),
    received_at: nullable(v.received_at, timestamp),
    original_event: parseProcessingEvent(v.original_event),
  };
};
function fieldValue(field: ChangeKey, value: unknown): ChangeValue {
  return nullable(value, (v) => {
    switch (field) {
      case 'auto_renew':
        return typeof v === 'boolean' ? v : invalid();
      case 'trial_end_date':
        return date(v);
      case 'amount':
        return typeof v === 'string' && /^\d{1,24}(\.\d{1,12})?$/.test(v)
          ? v
          : invalid();
      case 'currency':
        return typeof v === 'string' && /^[A-Z]{3}$/.test(v) ? v : invalid();
      case 'interval': {
        const i = record(v);
        return {
          unit: choice(i.unit, ['day', 'week', 'month', 'year'] as const),
          count: integer(i.count, 1, 10000),
        };
      }
      case 'contract_kind':
        return choice(v, ['recurring', 'fixed_term', 'unknown']);
      case 'effective_date':
      case 'access_end_date':
      case 'next_billing_date':
        return date(v);
      default:
        return text(v, 200);
    }
  });
}
export function parseChangePreview(value: unknown): ChangePreview {
  const v = record(value);
  const fields = array(
    v.fields,
    (raw) => {
      const f = record(raw),
        k = key(f.key);
      if (typeof f.selectable !== 'boolean') return invalid();
      return {
        key: k,
        current: fieldValue(k, f.current),
        proposed: fieldValue(k, f.proposed),
        selectable: f.selectable,
        reason: text(f.reason, 200, true),
      };
    },
    changeFields.length,
  );
  if (new Set(fields.map((f) => f.key)).size !== fields.length)
    return invalid();
  return {
    subscription: parseSubscription(v.subscription),
    source: evidence(v.source),
    additional_evidence: array(v.additional_evidence, evidence),
    preview_token: tokenValue(v.preview_token),
    as_of: date(v.as_of),
    timezone: choice(v.timezone, ['Asia/Tokyo']),
    algorithm_version: text(v.algorithm_version, 100),
    fields,
    warnings: array(v.warnings, (raw) => {
      const w = record(raw);
      return {
        code: text(w.code, 200),
        ...(w.field ? { field: key(w.field) } : {}),
      };
    }),
    derived_next_billing_date: nullable(v.derived_next_billing_date, date),
  };
}
export function parseChangeReceipt(value: unknown): ChangeReceipt {
  const v = record(value);
  const result = {
    request_id: uuid(v.request_id),
    applied_version: integer(v.applied_version, 1),
    created_at: timestamp(v.created_at),
    selected_fields: keys(v.selected_fields),
    before: parseSubscription(v.before),
    after: parseSubscription(v.after),
    source: evidence(v.source),
    additional_evidence: array(v.additional_evidence, evidence),
    algorithm_version: text(v.algorithm_version, 100),
  };
  return result.selected_fields.length &&
    result.before.id === result.after.id &&
    result.after.version === result.before.version + 1 &&
    result.applied_version === result.after.version
    ? result
    : invalid();
}
export function createSubscriptionChangesAPI(
  base: string,
  fetcher: typeof fetch = fetch,
) {
  const request = createDailyRequest(base, fetcher);
  const path = (id: string) => `/v1/subscriptions/${input(() => uuid(id))}`;
  const source = (v: ChangePreviewInput): ChangePreviewInput => ({
    expected_version: integer(v.expected_version, 1),
    source_result_id: uuid(v.source_result_id),
    source_event_index: integer(v.source_event_index, 0, 31),
    ...parseAdditionalSources(
      { result_id: v.source_result_id, event_index: v.source_event_index },
      v.additional_sources,
    ),
  });
  return {
    async preview(
      id: string,
      v: ChangePreviewInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<ChangePreview> {
      const result = parseChangePreview(
        await request(
          `${path(id)}/change-preview`,
          token,
          signal,
          'POST',
          input(() => source(v)),
        ),
      );
      return result.subscription.id === id &&
        result.subscription.version === v.expected_version &&
        result.source.result_id === v.source_result_id &&
        result.source.event_index === v.source_event_index
        ? result
        : invalid();
    },
    async apply(
      id: string,
      v: ChangeInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<ChangeResult> {
      const body = input(() => {
        const selected_fields = keys(v.selected_fields);
        if (!selected_fields.length || v.confirmed !== true) return invalid();
        return {
          ...source(v),
          request_id: uuid(v.request_id),
          preview_token: tokenValue(v.preview_token),
          selected_fields,
          confirmed: true,
        };
      });
      const result = record(
        await request(`${path(id)}/changes`, token, signal, 'POST', body),
      );
      const receipt = parseChangeReceipt(result.receipt),
        current = nullable(result.current, parseSubscription);
      return receipt.request_id === v.request_id &&
        receipt.before.id === id &&
        receipt.applied_version === v.expected_version + 1 &&
        receipt.source.result_id === v.source_result_id &&
        receipt.source.event_index === v.source_event_index &&
        JSON.stringify([...receipt.selected_fields].sort()) ===
          JSON.stringify([...v.selected_fields].sort()) &&
        (!current ||
          (current.id === id && current.version >= receipt.applied_version))
        ? { receipt, current }
        : invalid();
    },
    async list(
      id: string,
      offset: number,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<ChangePage> {
      const query = input(
        () =>
          new URLSearchParams({ limit: '20', offset: String(integer(offset)) }),
      );
      const v = record(
        await request(`${path(id)}/changes?${query}`, token, signal),
      );
      const changes = array(v.changes, parseChangeReceipt);
      return changes.every((change) => change.before.id === id)
        ? { changes, ...pagination(v) }
        : invalid();
    },
  };
}
export type SubscriptionChangesAPI = ReturnType<
  typeof createSubscriptionChangesAPI
>;
