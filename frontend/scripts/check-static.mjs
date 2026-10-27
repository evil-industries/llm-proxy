import assert from 'node:assert/strict';
import { readdir, readFile } from 'node:fs/promises';
import { join } from 'node:path';

async function files(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  return (
    await Promise.all(
      entries.map((entry) =>
        entry.isDirectory() ? files(join(directory, entry.name)) : join(directory, entry.name)
      )
    )
  ).flat();
}
const output = await files('build');
assert(output.includes('build/index.html'), 'SPA fallback must exist');
assert(output.some((name) => name.includes('/_app/immutable/') && name.endsWith('.js')));
assert(
  !output.some((name) => /(?:^|\/)(?:server|handler|env)\.(?:js|mjs)$/.test(name)),
  'No server runtime in static output'
);
for (const name of output) {
  if (!/\.(?:html|js|json|css)$/.test(name)) continue;
  const text = await readFile(name, 'utf8');
  for (const marker of [
    'AUTH_PASSWORD_HASH',
    'OIDC_CLIENT_SECRET',
    'PRIVATE_MANAGEMENT_KEY',
    '$env/dynamic/private',
    'node:crypto',
    'openid-client'
  ]) {
    assert(!text.includes(marker), `${name} contains server-only marker ${marker}`);
  }
  // Use distinctive dummy secrets during CI/build verification to catch accidental injection.
  for (const key of ['AUTH_PASSWORD_HASH', 'OIDC_CLIENT_SECRET', 'PRIVATE_MANAGEMENT_KEY']) {
    if (process.env[key])
      assert(!text.includes(process.env[key]), `${name} includes a secret value`);
  }
}
console.log(`Verified ${output.length} static files; no Node server or server-only configuration.`);
