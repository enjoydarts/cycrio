import { array, integer, invalid, record, uuid } from './daily-api.ts';
export type CandidateSource = { result_id: string; event_index: number };
export type CandidateSources = CandidateSource & {
  additional_sources?: CandidateSource[];
};
const key = (s: CandidateSource) => `${s.result_id}/${s.event_index}`;
export function candidateSourceKey(source: CandidateSources): string {
  return [key(source), ...(source.additional_sources ?? []).map(key)].join('+');
}
export function parseAdditionalSources(
  primary: CandidateSource | null,
  values?: CandidateSource[],
) {
  if (values === undefined) return {};
  if (!primary) return invalid();
  const additional_sources = array(
    values,
    (value) => {
      const v = record(value);
      return {
        result_id: uuid(v.result_id),
        event_index: integer(v.event_index, 0, 31),
      };
    },
    99,
  );
  const all = [primary, ...additional_sources];
  if (new Set(all.map(key)).size !== all.length) return invalid();
  return { additional_sources };
}
export function createCandidateSelection(anchor: CandidateSource) {
  const selected = new Map<string, CandidateSource>([
    [
      key(anchor),
      { result_id: anchor.result_id, event_index: anchor.event_index },
    ],
  ]);
  return {
    sources: () => [...selected.values()],
    has: (source: CandidateSource) => selected.has(key(source)),
    toggle(source: CandidateSource, checked: boolean) {
      if (checked && !selected.has(key(source)) && selected.size >= 100)
        return false;
      if (checked)
        selected.set(key(source), {
          result_id: source.result_id,
          event_index: source.event_index,
        });
      else selected.delete(key(source));
      return true;
    },
    selectPage(page: CandidateSource[]) {
      if (new Set([...selected.keys(), ...page.map(key)]).size > 100)
        return false;
      for (const source of page)
        selected.set(key(source), {
          result_id: source.result_id,
          event_index: source.event_index,
        });
      return true;
    },
    snapshot(): CandidateSources | null {
      const [primary, ...additional_sources] = selected.values();
      return primary
        ? {
            ...primary,
            ...(additional_sources.length ? { additional_sources } : {}),
          }
        : null;
    },
  };
}
