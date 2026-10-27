# @birge/ui

Design tokens and the shared components every Birge screen is built from ([ADR-0014](../../docs/adr/0014-ui-stack.md)). Screens compose these components; they never style React Native primitives themselves (ESLint enforces this in `apps/*/src/app` and `apps/*/src/screens`).

The components are adapted from [React Native Reusables](https://reactnativereusables.com) conventions (NativeWind classes, `cn()` merging, variant maps) and carry no text of their own: their few labels (SOS, loading, retry, close) come from the `ui` namespace of `@birge/i18n`.

## Tokens

| File                 | What                                                                                                                                        |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| `tokens.js`          | the source of every value: colors (light and dark), spacing (4-point grid), radii, font families, type ramp, touch target, motion durations |
| `tailwind-preset.js` | turns the tokens into NativeWind classes; colors become CSS variables, so one class works in both schemes                                   |
| `src/theme.ts`       | typed access (`tokens`, `useThemeColors()`) for props that need a raw value                                                                 |

The theme replaces Tailwind's default scales: `p-7`, `text-xl` or `bg-red-500` do not exist. Color classes are the token names in kebab case: `bg-background`, `text-muted-foreground`, `bg-danger-bg`, `border-separator`.

`src/theme.test.ts` checks WCAG 2.2 AA contrast (4.5:1 for text, 3:1 for input outlines) for every pair the components use, in both schemes, and that motion stays within 150–250 ms. Change a color there first if a check fails; never relax the test.

Font: Noto Sans (OFL-1.1), regular, semibold and bold, bundled from `@expo-google-fonts/noto-sans`. It covers Kazakh Cyrillic. The app loads `fonts` from this package at startup.

## Components

| Component                                  | Use it for                                            | Notes                                                                                                                                                                                                                                                                                                                         |
| ------------------------------------------ | ----------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Screen`                                   | the frame of every screen                             | safe area, background, level-1 title, the SOS entry, optional `footer` for the one primary action. `scroll={false}` when the content is a `List`. `variant="crisis"` for help/SOS screens: no animation inside, no SOS entry. `layout="form"` vertically centres title, body and footer as one keyboard-aware group (sign-in) |
| `Text`                                     | all text                                              | `variant`: body, caption, label, title, display; `tone`: default, muted, primary, destructive, `on*` for filled surfaces, info/success/warning/danger. Follows system font size, never truncates                                                                                                                              |
| `Heading`                                  | section (`level={2}`) and screen (`level={1}`) titles | announced as a header                                                                                                                                                                                                                                                                                                         |
| `Button`                                   | actions                                               | `variant`: primary (one per screen), secondary, destructive (SOS, irreversible). `loading` keeps the title, blocks presses, reports busy                                                                                                                                                                                      |
| `TextField`                                | text input                                            | `label` is required and is the accessibility label; `hint` below; `error` replaces the hint and is announced                                                                                                                                                                                                                  |
| `Card`                                     | a group of related content                            | not for every row (use `ListItem`); with `onPress` the whole card is one button and needs `accessibilityLabel`                                                                                                                                                                                                                |
| `ChoiceGroup`                              | exclusive choices (language, kind, scope)             | labelled radio row; selected uses the primary fill so the screen keeps one primary `Button`                                                                                                                                                                                                                                   |
| `ListItem`                                 | a row in a `List`                                     | title, optional subtitle and trailing element; `emphasis` for unread                                                                                                                                                                                                                                                          |
| `List`                                     | every scrolling list                                  | FlashList. Give it `data` (undefined until loaded), `loading`, `error`, `onRetry`, `empty`; it shows loading, error, empty or rows (with a non-blocking banner when a refresh failed). `header` scrolls with the rows                                                                                                         |
| `LoadingState`, `EmptyState`, `ErrorState` | the non-content states outside a `List`               | the same texts and accessibility everywhere                                                                                                                                                                                                                                                                                   |
| `Banner`                                   | a status message that stays on screen                 | warning and danger are announced as alerts                                                                                                                                                                                                                                                                                    |
| `Sheet`                                    | a short related task over the current screen          | for a task with its own route, prefer an Expo Router `formSheet`                                                                                                                                                                                                                                                              |
| `SosProvider`, `SosButton`                 | the SOS entry                                         | the app sets the destination once; `Screen` renders the button                                                                                                                                                                                                                                                                |

Motion: `useMotionAllowed()` is false under the OS reduce-motion setting and inside `NoMotion` (which `Screen variant="crisis"` adds). `useDuration(speed)` returns the token duration or 0. A component that animates must use them; the button spinner and the sheet transition already do.

## Adding a component

1. Check this package first, then [RN Reusables](https://reactnativereusables.com). A pattern used on two screens moves here.
2. Accept `variant`, `size`, state props and `className` (layout only); content goes in `children` or a few explicit props, not a growing list of content props.
3. Own its accessibility: role, label, state; text labels from the `ui` locale namespace.
4. Use tokens only; respect `useMotionAllowed()`.
5. Export it from `src/index.ts`, add a row to the table above and a test in `src/components.test.tsx`.
