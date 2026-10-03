# Shared UI primitives

These components come from the official [shadcn-svelte registry](https://shadcn-svelte.com/docs/components), using the project's `nova` style and existing aliases. Attribution is retained in [LICENSE](./LICENSE).

Dialog, Tabs, Select, Scroll Area, Card and Alert were generated with the official CLI in an isolated directory; only their files were copied into this project. Select also requires the Separator primitive. Dependencies were preserved; the existing Switch received the same Bits UI state-selector compatibility correction.

The installed Bits UI version exposes `data-state` and `data-orientation`. The new primitives use corresponding Tailwind selectors (`data-[state=active]`, `data-[orientation=horizontal]`, etc.) instead of registry shorthand that expects different attributes. Browser regression tests verify tab orientation, active styling, separator geometry, and Switch track color, thumb movement, and disabled behavior.

Every production component has a `.das.js` story. Context-dependent parts share a complete interactive `.example.svelte` composition so they render inside the required parent providers.

Garden 1.6.2 generates invalid JavaScript import bindings for hyphenated directories such as `scroll-area`. The Garden-only Vite transform in `garden.vite.config.js`, backed by `scripts/garden-generated.mjs`, normalizes only imported identifiers in the three generated map modules. Paths, labels and lookup keys remain unchanged; conflicting identifiers fail explicitly. Unit tests cover those guarantees.

## Semantic color and hierarchy

Keep the shell, navigation, and general primary actions neutral. Status accents are inspired by [Vercel Geist](https://vercel.com/geist/colors) and [Mastra](https://mastra.ai/), with darker text for light surfaces:

| Variant        | Meaning                       | Accent             | Usage                                                  |
| -------------- | ----------------------------- | ------------------ | ------------------------------------------------------ |
| `constructive` | Success, healthy, create/save | Green `#00652a`    | Add/save buttons, connected badges, healthy quota      |
| `destructive`  | Error, removal, critical      | Red `#d0001b`      | Delete actions, errors, quota at 10% or below          |
| `warning`      | Attention, pending, low quota | Amber `#fdac53`    | Awaiting approval, reconnecting, quota at 25% or below |
| `info`         | Information, authorization    | Sky blue `#6ccdfb` | Approval actions, information, connecting              |

Button, Badge, and Alert expose semantic variants. `trust` remains a compatibility alias for informational Button and Badge variants. Tokens live in `app.css`: the base color is accessible status ink, `-subtle` and `-border` support quiet surfaces, and `-accent` / `-accent-foreground` provide vivid filled actions. Keep text or icons alongside color. Unknown, disabled, and stale quota stays neutral; visual quota bands do not change notification thresholds or routing.

Keep major sections open on the page. Group content with spacing, headings, and subtle dividers; reserve bordered surfaces for inputs and focused transient flows.
