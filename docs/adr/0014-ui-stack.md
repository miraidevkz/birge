# ADR-0014: UI stack, motion and typography

Status: Accepted · Date: 2026-10-27

## Context
Users are tired, stressed parents, often on low-end Android phones and slow networks. The app must look trustworthy and native on both platforms, follow the OS look (including iOS Liquid Glass) and be accessible.

## Decision
- Components: React Native Reusables (shadcn-style, copied into `packages/ui`, built on `@rn-primitives`) styled with NativeWind v4.
- Motion: Reanimated and Gesture Handler (UI thread); Lottie for illustrations. 150–250 ms, no spring/bounce. Motion is off when the OS "reduce motion" setting is on. Crisis and SOS screens have no animation.
- Navigation: Expo Router with native stack transitions. Native platform materials where available (iOS Liquid Glass via Expo's glass/native-tab components), with a plain fallback on older iOS and on Android.
- Languages at launch: Kazakh, Russian and English (`kk`, `ru`, `en`); `kk-Latn` is added when the Latin alphabet transition requires it. All user-facing text lives in locale files; a new language needs no code change.
- Typography: a font family with full Kazakh Cyrillic coverage (PT Sans or Noto Sans, OFL).
- Accessibility: WCAG 2.2 AA contrast, system font scaling, VoiceOver/TalkBack labels, one primary action per screen, SOS reachable from every screen.
- Design source: Penpot.

## Reuse rule
- Screens are composed from `packages/ui` components only; a screen never styles raw primitives itself.
- A pattern used twice becomes a shared component; before writing a component, check `packages/ui` and RN Reusables first.
- Styling only through design tokens (NativeWind theme): no hard-coded colors, sizes, spacing, fonts or durations.
- Shared components own loading, empty and error states, accessibility labels and reduce-motion behaviour, so every screen gets them for free.
- Performance defaults live in the shared components: FlashList for lists, memoized rows, Reanimated on the UI thread, sized and cached images.

## Alternatives
- Tamagui: faster runtime styling, harder to configure, more errors in agent-written code.
- Flutter: rejected; non-native look erodes trust.

## Consequences
The design must not depend on glass effects: they exist only on recent iOS.
