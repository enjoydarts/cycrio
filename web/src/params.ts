import { uuid } from './lib/daily-api.ts';

function matchUUID(value: string): boolean {
  try {
    uuid(value);
    return true;
  } catch {
    return false;
  }
}

export const params = { uuid: matchUUID };
