import { getContext, setContext } from 'svelte';
import type { ErrorCode } from './api.ts';
import type { MailAccount } from './mail-api.ts';
import type { OverviewReport } from './overview-api.ts';

export type WorkspaceResource<T> = {
  readonly value: T | null;
  readonly status: 'idle' | 'loading' | 'ready' | 'error' | 'denied';
  readonly error: ErrorCode | null;
};
export type WorkspaceView = {
  readonly accounts: WorkspaceResource<MailAccount[]>;
  readonly overview: WorkspaceResource<OverviewReport>;
};
export type WorkspaceContext = {
  readonly view: WorkspaceView;
  refresh(): Promise<void>;
};
const workspace = Symbol('cycrio-workspace');
export function provideWorkspace(value: WorkspaceContext) {
  setContext(workspace, value);
}
export function getWorkspace(): WorkspaceContext | undefined {
  return getContext(workspace);
}
