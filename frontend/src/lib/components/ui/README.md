# Shared UI primitives

These components come from the official [shadcn-svelte registry](https://shadcn-svelte.com/docs/components), using the project's `nova` style and existing aliases. Attribution is retained in [LICENSE](./LICENSE).

Dialog, Tabs, Select, Scroll Area, Card and Alert were generated with the official CLI in an isolated directory; only their files were copied into this project. Select also requires the Separator primitive. Dependencies were preserved; the existing Switch received the same Bits UI state-selector compatibility correction.

The installed Bits UI version exposes `data-state` and `data-orientation`. The new primitives use corresponding Tailwind selectors (`data-[state=active]`, `data-[orientation=horizontal]`, etc.) instead of registry shorthand that expects different attributes. Browser regression tests verify tab orientation, active styling, separator geometry, and Switch track color, thumb movement, and disabled behavior.

Every production component has a `.das.js` story. Context-dependent parts share a complete interactive `.example.svelte` composition so they render inside the required parent providers.

Garden 1.6.2 generates invalid JavaScript import bindings for hyphenated directories such as `scroll-area`. The Garden-only Vite transform in `garden.vite.config.js`, backed by `scripts/garden-generated.mjs`, normalizes only imported identifiers in the three generated map modules. Paths, labels and lookup keys remain unchanged; conflicting identifiers fail explicitly. Unit tests cover those guarantees.
