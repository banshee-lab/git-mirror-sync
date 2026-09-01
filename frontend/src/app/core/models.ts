export interface Repository {
  id: number;
  name: string;
  pull_url_raw: string;
  pull_url_ssh: string;
  pull_ssh_key_id: number;
  push_url_raw: string;
  push_url_ssh: string;
  push_ssh_key_id: number;
  provider_hint?: string;
  push_provider_hint?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  last_sync_at?: string;
  last_sync_status?: string;
}

export interface SSHKey {
  id: number;
  name: string;
  key_type: 'ed25519' | 'rsa' | string;
  fingerprint: string;
  private_path?: string;
  public_path?: string;
  created_at?: string;
}

export interface SSHKeyOption {
  id: number;
  name: string;
  key_type: string;
}

export interface HistoryEvent {
  id: number;
  received_at?: string;
  finished_at?: string;
  provider?: string;
  repo_full_name?: string;
  clone_url?: string;
  ssh_url?: string;
  ref?: string;
  commit_before?: string;
  commit_after?: string;
  status: string;
  detail?: string;
  duration_ms?: number;
}

export interface PagedResult<T> {
  items: T[];
  total: number;
  page: number;
  page_size: number;
}

export interface ApiErrorResponse {
  error?: {
    code?: string;
    message?: string;
  };
  message?: string;
}

export interface DirectionTestResult {
  ok: boolean;
  message: string;
}

export interface ConnectionTestResult {
  pull: DirectionTestResult;
  push: DirectionTestResult;
}

export interface SyncSseEvent {
  type: 'sync_started' | 'sync_completed' | 'sync_failed' | string;
  event_id?: number;
  repo_id?: number;
  repo_name?: string;
  status: 'running' | 'success' | 'error' | 'conflict' | 'skipped' | string;
  detail?: string;
  duration_ms?: number;
  timestamp?: string;
}

