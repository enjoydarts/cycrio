import { ApiError } from './api.ts';
import type { Candidate, CandidatePage } from './approval-api.ts';
export async function loadCandidateListSelection(
  read: (offset: number) => Promise<CandidatePage>,
  signal: AbortSignal,
): Promise<Candidate[]> {
  signal.throwIfAborted();
  let offset = 0,
    total: number | undefined;
  const selected = new Map<string, Candidate>();
  while (true) {
    const page = await read(offset);
    signal.throwIfAborted();
    if (
      page.offset !== offset ||
      page.limit < 1 ||
      (total !== undefined && total !== page.total)
    )
      throw new ApiError('conflict');
    total = page.total;
    for (const row of page.candidates)
      selected.set(`${row.result_id}/${row.event_index}`, row);
    if (offset + page.limit >= total) break;
    if (!page.candidates.length) throw new ApiError('conflict');
    offset += page.limit;
  }
  if (selected.size !== total) throw new ApiError('conflict');
  return [...selected.values()];
}
