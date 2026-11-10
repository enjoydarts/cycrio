import { defineParams } from '@sveltejs/kit/params';
import { uuid } from './lib/daily-api.ts';

function matchUUID(value: string): string | undefined {
  try {
    return uuid(value);
  } catch {
    return undefined;
  }
}

export const params = defineParams({ uuid: matchUUID });
