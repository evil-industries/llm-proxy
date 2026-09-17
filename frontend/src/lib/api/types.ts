export type RoutingStrategy = 'round-robin' | 'weighted-round-robin' | 'fill-first';

export interface NotificationSettings {
  enabled: boolean;
  reset_notifications: boolean;
  banked_reset_notifications: boolean;
  url: string;
  topic: string;
  warning_percent: number;
  critical_percent: number;
  token_configured: boolean;
  status: {
    enabled: boolean;
    configured: boolean;
    in_flight: boolean;
    last_success_at?: string;
    last_error?: string;
    next_retry_at?: string;
  };
}

export type NotificationUpdate = Pick<
  NotificationSettings,
  | 'enabled'
  | 'reset_notifications'
  | 'banked_reset_notifications'
  | 'url'
  | 'topic'
  | 'warning_percent'
  | 'critical_percent'
> & { token?: string; clear_token?: boolean };

export type BooleanSetting =
  | 'debug'
  | 'logging-to-file'
  | 'usage-statistics-enabled'
  | 'request-log'
  | 'ws-auth'
  | 'force-model-prefix'
  | 'quota-exceeded/switch-project'
  | 'quota-exceeded/switch-preview-model';

export interface ManagementConfig {
  debug?: boolean;
  'logging-to-file'?: boolean;
  'usage-statistics-enabled'?: boolean;
  'request-log'?: boolean;
  'ws-auth'?: boolean;
  'force-model-prefix'?: boolean;
  'api-keys'?: string[] | null;
  'request-retry'?: number;
  'max-retry-credentials'?: number;
  'max-retry-interval'?: number;
  routing?: { strategy?: RoutingStrategy };
  'quota-exceeded'?: {
    'switch-project'?: boolean;
    'switch-preview-model'?: boolean;
  };
  [key: string]: unknown;
}

export interface RecentRequestBucket {
  time: string;
  /** Unix seconds identifying the interval, independent of the server local timezone. */
  timestamp?: number;
  success: number;
  failed: number;
}

export interface AuthFile {
  name: string;
  id?: string;
  auth_index?: string;
  provider?: string;
  type?: string;
  label?: string;
  email?: string;
  status?: string;
  status_message?: string;
  disabled?: boolean;
  unavailable?: boolean;
  runtime_only?: boolean;
  source?: string;
  size?: number;
  modtime?: string;
  created_at?: string;
  success?: number;
  failed?: number;
  recent_requests?: RecentRequestBucket[];
  account_type?: string;
  [key: string]: unknown;
}

export interface APIKeyUsageEntry {
  success: number;
  failed: number;
  recent_requests: RecentRequestBucket[];
}

/** Composite keys contain provider credentials; never display or persist them. */
export type APIKeyUsage = Record<string, Record<string, APIKeyUsageEntry>>;

export interface Plugin {
  id: string;
  path: string;
  configured: boolean;
  registered: boolean;
  enabled: boolean;
  effective_enabled: boolean;
  supports_oauth: boolean;
  oauth_provider: string;
  supports_quota: boolean;
  quota_provider?: string;
  logo: string;
  metadata: {
    name: string;
    version: string;
    author: string;
    github_repository: string;
    logo: string;
  } | null;
}

export interface PluginList {
  plugins_enabled: boolean;
  plugins_dir: string;
  plugins: Plugin[];
}

export interface LogsResponse {
  lines: string[];
  'line-count': number;
  'latest-timestamp': number;
  'next-cursor': string;
  'cursor-reset'?: boolean;
}

export interface LogsQuery {
  limit?: number;
  cursor?: string;
  after?: number;
}
