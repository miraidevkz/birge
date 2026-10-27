// ESLint flat config for all client workspaces.
const { defineConfig } = require('eslint/config');
const expoConfig = require('eslint-config-expo/flat');

// ADR-0014: screens are composed from @birge/ui components only, and styling
// goes through design tokens. These checks catch the common slips; review
// catches the rest.
const rawPrimitives = [
  'Text',
  'View',
  'ScrollView',
  'Pressable',
  'TouchableOpacity',
  'TextInput',
  'FlatList',
  'Image',
  'StyleSheet',
];

module.exports = defineConfig([
  expoConfig,
  {
    settings: {
      // Resolve the "@/..." alias and workspace packages the way TypeScript does.
      'import/resolver': {
        typescript: { project: ['apps/*/tsconfig.json', 'packages/*/tsconfig.json'] },
      },
    },
  },
  {
    // Only the client workspaces and the root configs are JavaScript; the rest of the repository is Go.
    ignores: [
      '**/node_modules/**',
      '**/dist/**',
      '**/.expo/**',
      'packages/api/src/schema.gen.ts',
      'bin/**',
      'cmd/**',
      'contracts/**',
      'db/**',
      'docs/**',
      'evals/**',
      'internal/**',
      'presets/**',
      'tools/**',
    ],
  },
  {
    files: ['apps/*/src/app/**/*.{ts,tsx}', 'apps/*/src/screens/**/*.{ts,tsx}'],
    ignores: ['**/*.test.{ts,tsx}'],
    rules: {
      'no-restricted-imports': [
        'error',
        {
          paths: [
            {
              name: 'react-native',
              importNames: rawPrimitives,
              message: 'Screens use @birge/ui components, not raw primitives (ADR-0014).',
            },
          ],
        },
      ],
      'no-restricted-syntax': [
        'error',
        {
          selector: 'Literal[value=/#[0-9a-fA-F]{3,8}\\b/]',
          message: 'No hard-coded colors in screens: use design tokens from @birge/ui (ADR-0014).',
        },
        {
          selector: 'JSXAttribute[name.name="style"][value.type="JSXExpressionContainer"]',
          message: 'Screens do not style components: add a variant to the @birge/ui component instead (ADR-0014).',
        },
      ],
    },
  },
  {
    // Message text and personal data must never reach logs (AGENTS.md security rules).
    files: ['apps/**/*.{ts,tsx}', 'packages/**/*.{ts,tsx}'],
    ignores: ['**/*.test.{ts,tsx}', 'packages/api/scripts/**'],
    rules: {
      'no-console': 'error',
    },
  },
  {
    // Jest hoists jest.mock above imports, so mock factories must use require().
    files: ['**/*.test.{ts,tsx}', 'jest.setup.js'],
    languageOptions: { globals: { jest: 'readonly' } },
    rules: { '@typescript-eslint/no-require-imports': 'off' },
  },
]);
