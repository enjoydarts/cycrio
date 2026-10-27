import type {
  ProcessingPreview,
  ProcessingRun,
  ProcessingResult,
  PolicyState,
} from './mail-processing-api.ts';
import {
  parseProcessingPreview,
  parseProcessingRun,
  parseProcessingResult,
  parsePolicyState,
} from './mail-processing-api.ts';
import serializedGo from './mail-processing-go-fixtures.json' with { type: 'json' };
// The shape originates in Go's JSON encoder; each test changes only its scenario.
const go = {
  preview: parseProcessingPreview(
    serializedGo.preview,
    serializedGo.account.id,
  ),
  run: parseProcessingRun(serializedGo.queued, serializedGo.account.id),
  result: parseProcessingResult(serializedGo.unknown),
  policy: parsePolicyState(serializedGo.policy),
};
export const processingIDs = {
  account: '11111111-1111-4111-8111-111111111111',
  profile: '22222222-2222-4222-8222-222222222222',
  request: '33333333-3333-4333-8333-333333333333',
  run: '44444444-4444-4444-8444-444444444444',
  message: '55555555-5555-4555-8555-555555555555',
  result: '66666666-6666-4666-8666-666666666666',
};
export const publicModels = () => ({
  primary: {
    ...structuredClone(go.preview.primary!),
    price: null,
    retention: '契約で保持条件を確認済み',
    routing_provider: '',
  },
  secondary: {
    ...structuredClone(go.preview.secondary!),
    provider: 'openrouter' as const,
    model_id: 'example/model',
    display_name: '抽出モデル',
    price: null,
    retention: 'ZDR の契約条件を確認済み',
    routing_provider: 'approved/provider',
  },
});
export const processingPreview = (
  patch: Partial<ProcessingPreview> = {},
): ProcessingPreview => ({
  ...structuredClone(go.preview),
  full_email_allowed: true,
  account_id: processingIDs.account,
  expected_version: 1,
  runtime_version: 2,
  profile_id: processingIDs.profile,
  policy_version: 3,
  pending_count: 6,
  failed_count: 1,
  unknown_count: 1,
  ...publicModels(),
  max_output_tokens: 1024,
  ...patch,
});
export const processingRun = (
  patch: Partial<ProcessingRun> = {},
): ProcessingRun => ({
  ...structuredClone(go.run),
  id: processingIDs.run,
  account_id: processingIDs.account,
  ...patch,
});
export const processingResult = (
  patch: Partial<ProcessingResult> = {},
): ProcessingResult => ({
  ...structuredClone(go.result),
  id: processingIDs.result,
  message_id: processingIDs.message,
  ...patch,
});
export const processingPolicy = (
  patch: Partial<PolicyState> = {},
): PolicyState => ({
  ...structuredClone(go.policy),
  runtime_version: 2,
  profile: {
    ...structuredClone(go.policy.profile!),
    id: processingIDs.profile,
    ...publicModels(),
    max_output_tokens: 1024,
  },
  policy: {
    ...structuredClone(go.policy.policy!),
    profile_id: processingIDs.profile,
    version: 3,
    enabled: false,
    primary_retention: '一次条件',
    secondary_retention: '二次条件',
    primary_confirmed: false,
    secondary_confirmed: false,
  },
  ...patch,
});
