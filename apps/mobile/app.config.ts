// Expo app configuration. Everything deployment-specific comes from the
// environment (or apps/mobile/.env, see .env.example), so one source tree
// serves every country deployment. Only public values belong here: the whole
// `extra` object is embedded in the bundle and readable by anyone.
import type { ConfigContext, ExpoConfig } from 'expo/config';

const env = process.env;

export default ({ config }: ConfigContext): ExpoConfig => ({
  ...config,
  name: 'Birge',
  slug: 'birge',
  version: '0.1.0',
  scheme: 'birge',
  orientation: 'portrait',
  userInterfaceStyle: 'automatic',
  ios: {
    // The store identifier belongs to the operator of a deployment.
    bundleIdentifier: env.BIRGE_APP_ID ?? 'invalid.example.birge',
    supportsTablet: true,
  },
  android: {
    package: env.BIRGE_APP_ID ?? 'invalid.example.birge',
  },
  web: {
    bundler: 'metro',
    // A single-page app: routes render in the browser, not at build time, so
    // nothing needs device APIs during the build. public/index.html links the
    // PWA manifest.
    output: 'single',
    name: 'Birge',
    shortName: 'Birge',
  },
  plugins: ['expo-router', 'expo-secure-store', 'expo-localization', 'expo-font'],
  extra: {
    // Read by src/lib/config.ts, which validates them.
    apiUrl: env.EXPO_PUBLIC_API_URL ?? 'http://localhost:8080/v1',
    kratosUrl: env.EXPO_PUBLIC_KRATOS_URL ?? 'http://localhost:8080/auth',
    // Crisis numbers shown before the app has ever reached the server, as JSON
    // [{"number":"...","label":"..."}]. Empty by default: they are country data.
    crisisPhones: env.EXPO_PUBLIC_CRISIS_PHONES ?? '[]',
  },
});
