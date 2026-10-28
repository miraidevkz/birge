# ADR-0025: Warm design language

Status: Accepted · Date: 2026-10-28 · Extends [ADR-0014](0014-ui-stack.md)

## Context
The first client build worked but looked like a developer's default UI: one type size and weight everywhere, flat bordered boxes, text-only empty states, a cool grey palette and content stretched across wide web windows. Nothing told a tired mother "this is a calm place for you", and nothing guided the eye to the one thing to do next. The owner chose one concept for the whole project instead of per-screen variants: warm and homely, in line with established practice for wellbeing and peer-support apps (calm palette, generous space, large friendly headings, soft surfaces, one clear action).

## Decision
One design language, set in `packages/ui/tokens.js` and the shared components, never per screen:

- **Palette.** Warm cream ground (`background`) and white cards; the sage `primary` stays the brand colour; a warm sand `accent` marks gentle positive moments (greetings, empty states, tips). Every text pair meets WCAG 2.2 AA (4.5:1) in light and dark; red is reserved for real danger.
- **Type.** Two OFL families with full Kazakh Cyrillic: Nunito (rounded, warm) for headings (`display`, `title`), Noto Sans for reading text. A clear ramp: display 30/38, title 21/28, body 17/26, label 16/22, caption 14/20. Each screen has exactly one display-size heading.
- **Shape.** Soft radii (cards 24, controls 16, buttons fully rounded); cards are raised with a faint shadow instead of a hard border.
- **Space.** Generous padding (cards 20, screen gutters 20); on wide screens content sits in one centred column (`max-w-content`, 640 px) instead of stretching.
- **Warmth without noise.** Empty states get a soft icon in a tinted circle and a kind sentence; the home screen greets the member by name and puts today's one action first. Motion stays within ADR-0014 (150–250 ms, none with reduce motion and on crisis screens).
- **Discretion.** Nothing in the look reveals the app's subject to someone glancing at the phone (same rule as the app icon).

## Consequences
New screens inherit the look by using `packages/ui`; a screen that needs a new visual pattern adds it to `packages/ui` first. Changing the language means changing tokens and components, and this ADR. Locale texts stay where they are; the kind tone of empty states and greetings is part of the copy review by a native speaker and the psychologist.
