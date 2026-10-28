# @birge/i18n

Every user-visible string of the clients, in Kazakh, Russian and English ([ADR-0014](../../docs/adr/0014-ui-stack.md)). Code never contains UI text; it calls `useTranslation(namespace)` from this package.

Write and review texts by the tone guide [`docs/content-style.md`](../../docs/content-style.md); it also lists the texts excluded from tone edits (questionnaire, crisis, legal).

- One JSON file per feature namespace in `src/locales/<lang>/`; the list is in `src/resources.ts`. The staff console uses the `admin` namespace (with nested keys per screen).
- Language: the first supported device language, otherwise Kazakh. Missing keys fall back to Kazakh.
- Texts are bundled, so they work offline, including the fixed crisis screen.
- `src/locales.test.ts` fails when a language misses a key or a file, a value is empty, or placeholders (`{{name}}`) differ between languages.

## Add a key

1. Add it to the namespace file in `kk/`, `ru/` and `en/`, at the same path.
2. Use it: `const { t } = useTranslation('home'); t('checkin.heading')`.
3. `npm test` checks the three files agree.

## Add a namespace

Create `<ns>.json` in all three language folders, import them in `src/resources.ts` and add the name to `namespaces`.

## Add a language

Add the folder with every namespace, add it to `languages` in `src/index.ts` and to `resources`; no screen changes.

The crisis texts (`help` namespace) need a review by a psychologist and a native speaker of each language before release; change their meaning only with such a review.
