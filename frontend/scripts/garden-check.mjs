import assert from 'node:assert/strict';
import { readdir, access } from 'node:fs/promises';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const componentDirectory = join(root, 'src');

async function collect(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = await Promise.all(
    entries.map((entry) => {
      const path = join(directory, entry.name);
      return entry.isDirectory() ? collect(path) : [path];
    })
  );
  return files.flat();
}

// Auxiliary example components are story fixtures, not production components.
const files = await collect(componentDirectory);
const components = files.filter(
  (file) => file.endsWith('.svelte') && !file.endsWith('.example.svelte')
);
assert(components.length > 0, 'No components found; story coverage cannot be checked.');

const failures = [];
for (const component of components) {
  const name = relative(root, component);
  try {
    const storyPath = component
      .replace(/([/\\])\+([^/\\]+)\.svelte$/, '$1$2.das.js')
      .replace(/\.svelte$/, '.das.js');
    await access(storyPath);
    const { default: story } = await import(pathToFileURL(storyPath).href);
    assert(
      Array.isArray(story?.examples) && story.examples.length > 0,
      'at least one example is required'
    );
    assert(
      story.examples.every((example) => typeof example.title === 'string' && example.title.trim()),
      'every example needs a title'
    );
    await access(
      resolve(dirname(storyPath), story.file ?? relative(dirname(component), component))
    );
  } catch (error) {
    failures.push(`${name}: ${error.message}`);
  }
}

if (failures.length) {
  console.error(`Garden story coverage failed:\n${failures.join('\n')}`);
  process.exitCode = 1;
} else {
  console.log(`Garden stories cover all ${components.length} production components.`);
}
