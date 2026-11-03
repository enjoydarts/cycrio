import type { ProcessingEvent } from './mail-processing-api.ts';
import type { SubscriptionValues } from './subscriptions-api.ts';

export function extractedCharge(
  event: ProcessingEvent,
  contractKind: SubscriptionValues['contract_kind'],
): Pick<SubscriptionValues, 'amount' | 'currency'> | null {
  if (
    contractKind === 'fixed_term' ||
    event.contract_kind === 'fixed_term' ||
    event.event_type === 'refund' ||
    event.event_type === 'cancellation' ||
    event.amount === null ||
    event.recurring_amount != null
  )
    return null;
  return { amount: event.amount, currency: event.currency };
}
