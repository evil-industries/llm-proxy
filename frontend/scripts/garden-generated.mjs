/**
 * Garden 1.6.2 uses directory names as JavaScript import bindings verbatim.
 * Normalize hyphens only in those bindings; preserve paths, labels and map keys.
 * @param {string} code
 * @returns {string}
 */
export function sanitizeGardenImports(code) {
  const imports = [...code.matchAll(/^import\s+([A-Za-z_$][\w$-]*)\s+from\s/gm)].map(
    (match) => match[1]
  );
  const names = new Set(imports);
  const replacements = new Map();
  for (const name of imports) {
    if (!name.includes('-')) continue;
    const safe = name.replaceAll('-', '_');
    if (names.has(safe)) throw new Error(`Garden component binding collision: ${safe}`);
    names.add(safe);
    replacements.set(name, safe);
  }
  if (!replacements.size) return code;

  // Skip string literals and comments so routes and displayed component names stay intact.
  return code.replace(
    /("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`|\/\/[^\n]*|\/\*[\s\S]*?\*\/)|([A-Za-z_$][\w$-]*)/g,
    (match, literal, identifier) => literal ?? replacements.get(identifier) ?? match
  );
}
