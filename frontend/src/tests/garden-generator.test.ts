import { describe, expect, test } from 'vitest';
import { sanitizeGardenImports } from '../../scripts/garden-generated.mjs';

describe('Garden generated import bindings', () => {
  test('normalizes hyphenated bindings without modifying paths, labels, or lookup keys', () => {
    const source = `import ComponentsUiScroll-areaScrollArea from '../src/lib/components/ui/scroll-area/ScrollArea.example.svelte';
import ComponentsUiScroll-areaDas from '../src/lib/components/ui/scroll-area/scroll-area.das.js';
export const componentMap = {'ComponentsUiScroll-areaScrollArea': ComponentsUiScroll-areaScrollArea};
export const dasMap = {'ComponentsUiScroll-areaDas': {...ComponentsUiScroll-areaDas, label: 'Scroll-area'}};
// ComponentsUiScroll-areaScrollArea
`;
    expect(sanitizeGardenImports(source))
      .toBe(`import ComponentsUiScroll_areaScrollArea from '../src/lib/components/ui/scroll-area/ScrollArea.example.svelte';
import ComponentsUiScroll_areaDas from '../src/lib/components/ui/scroll-area/scroll-area.das.js';
export const componentMap = {'ComponentsUiScroll-areaScrollArea': ComponentsUiScroll_areaScrollArea};
export const dasMap = {'ComponentsUiScroll-areaDas': {...ComponentsUiScroll_areaDas, label: 'Scroll-area'}};
// ComponentsUiScroll-areaScrollArea
`);
  });
  test('leaves existing valid modules untouched', () => {
    const source = "import Button from './button.svelte'; export const map = { Button };";
    expect(sanitizeGardenImports(source)).toBe(source);
  });
  test('rejects ambiguous generated binding collisions', () => {
    expect(() =>
      sanitizeGardenImports("import A-b from './one';\nimport A_b from './two';")
    ).toThrow('collision');
  });
});
