import {
  createManagementClient,
  type AuthFile,
  type ManagementConfig,
  type NotificationSettings
} from './api';

export const demoNotifications: NotificationSettings = {
  enabled: false,
  reset_notifications: true,
  banked_reset_notifications: true,
  url: '',
  topic: '',
  warning_percent: 20,
  critical_percent: 5,
  token_configured: false,
  status: { enabled: false, configured: false, in_flight: false }
};

export const demoFiles: AuthFile[] = [
  {
    name: 'anthropic-team.json',
    provider: 'claude',
    quota: {
      observed_at: new Date().toISOString(),
      signals: {
        'anthropic-ratelimit-unified-5h-utilization': '0.32',
        'anthropic-ratelimit-unified-5h-reset': String(Math.floor(Date.now() / 1000) + 7200),
        'anthropic-ratelimit-unified-7d-utilization': '0.58',
        'anthropic-ratelimit-unified-7d-reset': String(Math.floor(Date.now() / 1000) + 172800)
      }
    },
    email: 'team@example.com',
    status: 'ready',
    disabled: false,
    success: 18426,
    failed: 23,
    recent_requests: Array.from({ length: 20 }, (_, index) => {
      const start = 360 + index * 10;
      const label = (minutes: number) =>
        `${String(Math.floor(minutes / 60)).padStart(2, '0')}:${String(minutes % 60).padStart(2, '0')}`;
      return {
        time: `${label(start)}-${label(start + 10)}`,
        success: [16, 22, 18, 32, 29, 41, 48, 36, 55, 42][index % 10] * 3,
        failed: index % 7 === 0 ? 3 : 0
      };
    })
  },
  {
    name: 'codex-workspace.json',
    provider: 'codex',
    quota: {
      observed_at: new Date().toISOString(),
      signals: {
        'x-codex-primary-used-percent': '24',
        'x-codex-primary-window-minutes': '300',
        'x-codex-primary-reset-at': String(Math.floor(Date.now() / 1000) + 3600),
        'x-codex-secondary-used-percent': '87',
        'x-codex-secondary-window-minutes': '10080',
        'x-codex-secondary-reset-at': String(Math.floor(Date.now() / 1000) + 259200)
      }
    },
    email: 'workspace@example.com',
    status: 'ready',
    disabled: false,
    success: 12058,
    failed: 12,
    recent_requests: Array.from({ length: 20 }, (_, index) => {
      const start = 360 + index * 10;
      const label = (minutes: number) =>
        `${String(Math.floor(minutes / 60)).padStart(2, '0')}:${String(minutes % 60).padStart(2, '0')}`;
      return {
        time: `${label(start)}-${label(start + 10)}`,
        success: [16, 22, 18, 32, 29, 41, 48, 36, 55, 42][index % 10] * 2,
        failed: index % 7 === 0 ? 2 : 0
      };
    })
  },
  {
    name: 'gemini-production.json',
    provider: 'gemini',
    email: 'production@example.com',
    status: 'ready',
    disabled: false,
    success: 6834,
    failed: 4,
    recent_requests: Array.from({ length: 20 }, (_, index) => {
      const start = 360 + index * 10;
      const label = (minutes: number) =>
        `${String(Math.floor(minutes / 60)).padStart(2, '0')}:${String(minutes % 60).padStart(2, '0')}`;
      return {
        time: `${label(start)}-${label(start + 10)}`,
        success: [16, 22, 18, 32, 29, 41, 48, 36, 55, 42][index % 10] * 1,
        failed: index % 7 === 0 ? 1 : 0
      };
    })
  },
  {
    name: 'codex-backup.json',
    provider: 'codex',
    email: 'backup@example.com',
    status: 'disabled',
    disabled: true,
    success: 412,
    failed: 0,
    recent_requests: Array.from({ length: 20 }, (_, index) => {
      const start = 360 + index * 10;
      const label = (minutes: number) =>
        `${String(Math.floor(minutes / 60)).padStart(2, '0')}:${String(minutes % 60).padStart(2, '0')}`;
      return {
        time: `${label(start)}-${label(start + 10)}`,
        success: [16, 22, 18, 32, 29, 41, 48, 36, 55, 42][index % 10] * 0,
        failed: index % 7 === 0 ? 0 : 0
      };
    })
  }
];
export const demoKeys = [
  'demo-key-workspace-not-a-real-secret',
  'demo-key-development-not-a-real-secret'
];
export const demoConfig: ManagementConfig = {
  routing: { strategy: 'round-robin' },
  debug: false,
  'logging-to-file': true,
  'usage-statistics-enabled': true,
  'request-log': false,
  'ws-auth': true
};
export const demoLogs = [
  '[09:42:08] [info] CLIProxyAPI server started on :8317',
  '[09:42:08] [info] Management routes registered',
  '[09:42:09] [info] Credential watcher started · 4 credentials loaded',
  '[09:43:12] [info] POST /v1/responses 200 · codex',
  '[09:43:18] [info] POST /v1/messages 200 · claude',
  '[09:43:24] [info] POST /v1/chat/completions 200 · gemini'
];
export const demoClient = createManagementClient({
  baseUrl: '',
  managementKey: 'demo',
  fetch: async () => {
    throw new Error('Demo mode is read-only.');
  }
});
