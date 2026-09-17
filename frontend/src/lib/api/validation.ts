type ObjectValue = Record<string, unknown>;
type Validator = (value: unknown) => boolean;

const object = (value: unknown): value is ObjectValue =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const string: Validator = (value) => typeof value === 'string';
const boolean: Validator = (value) => typeof value === 'boolean';
const number: Validator = (value) => typeof value === 'number' && Number.isFinite(value);
const count: Validator = (value) => number(value) && (value as number) >= 0;
const array =
  (validate: Validator): Validator =>
  (value) =>
    Array.isArray(value) && value.every(validate);
const nullable =
  (validate: Validator): Validator =>
  (value) =>
    value === null || validate(value);
const fields = (value: ObjectValue, names: string[], validate: Validator) =>
  names.every((name) => !(name in value) || validate(value[name]));

const bucket: Validator = (value) =>
  object(value) && string(value.time) && count(value.success) && count(value.failed);
const usageEntry: Validator = (value) =>
  object(value) &&
  count(value.success) &&
  count(value.failed) &&
  array(bucket)(value.recent_requests);

const authFile: Validator = (value) =>
  object(value) &&
  string(value.name) &&
  fields(
    value,
    [
      'id',
      'auth_index',
      'provider',
      'type',
      'label',
      'email',
      'status',
      'status_message',
      'source',
      'modtime',
      'created_at',
      'account_type'
    ],
    string
  ) &&
  fields(value, ['disabled', 'unavailable', 'runtime_only'], boolean) &&
  fields(value, ['success', 'failed', 'size'], count) &&
  fields(value, ['recent_requests'], array(bucket));

const config: Validator = (value) =>
  object(value) &&
  fields(
    value,
    [
      'debug',
      'logging-to-file',
      'usage-statistics-enabled',
      'request-log',
      'ws-auth',
      'force-model-prefix'
    ],
    boolean
  ) &&
  fields(value, ['request-retry', 'max-retry-credentials', 'max-retry-interval'], number) &&
  fields(value, ['api-keys'], nullable(array(string))) &&
  fields(
    value,
    ['routing'],
    (routing) => object(routing) && fields(routing, ['strategy'], string)
  ) &&
  fields(
    value,
    ['quota-exceeded'],
    (quota) => object(quota) && fields(quota, ['switch-project', 'switch-preview-model'], boolean)
  );

const plugin: Validator = (value) =>
  object(value) &&
  string(value.id) &&
  [
    'configured',
    'registered',
    'enabled',
    'effective_enabled',
    'supports_oauth',
    'supports_quota'
  ].every((key) => boolean(value[key])) &&
  fields(value, ['path', 'oauth_provider', 'quota_provider', 'logo'], string) &&
  nullable(
    (metadata) =>
      object(metadata) &&
      fields(metadata, ['name', 'version', 'author', 'github_repository', 'logo'], string)
  )(value.metadata);

/** Validate fields consumed by the UI while allowing new server fields. */
export function validManagementResponse(path: string, value: unknown): boolean {
  switch (path.split('?')[0]) {
    case '/notifications':
      return (
        object(value) &&
        boolean(value.enabled) &&
        boolean(value.reset_notifications) &&
        boolean(value.banked_reset_notifications) &&
        string(value.url) &&
        string(value.topic) &&
        count(value.warning_percent) &&
        (value.warning_percent as number) <= 100 &&
        count(value.critical_percent) &&
        (value.critical_percent as number) < (value.warning_percent as number) &&
        boolean(value.token_configured) &&
        object(value.status) &&
        boolean(value.status.enabled) &&
        boolean(value.status.configured) &&
        boolean(value.status.in_flight) &&
        fields(value.status, ['last_success_at', 'last_error', 'next_retry_at'], string)
      );
    case '/config':
      return config(value);
    case '/auth-files':
      return object(value) && nullable(array(authFile))(value.files);
    case '/api-keys':
      return object(value) && nullable(array(string))(value['api-keys']);
    case '/api-key-usage':
      return (
        object(value) &&
        Object.values(value).every(
          (provider) => object(provider) && Object.values(provider).every(usageEntry)
        )
      );
    case '/plugins':
      return (
        object(value) &&
        boolean(value.plugins_enabled) &&
        string(value.plugins_dir) &&
        array(plugin)(value.plugins)
      );
    case '/logs':
      return (
        object(value) &&
        array(string)(value.lines) &&
        count(value['line-count']) &&
        count(value['latest-timestamp']) &&
        string(value['next-cursor']) &&
        fields(value, ['cursor-reset'], boolean)
      );
    case '/routing/strategy':
      return object(value) && string(value.strategy);
    default:
      return true;
  }
}
