// Expo app configuration for the staff admin (web only, ADR-0004). Everything
// deployment-specific comes from the environment (or apps/admin/.env); only
// public values belong here: the whole `extra` object is embedded in the bundle.
import type { ConfigContext, ExpoConfig } from 'expo/config';

const env = process.env;

// The admin is served under this path, next to the member app on the same
// origin, so the stack needs one public host. Also the OIDC redirect path.
const basePath = '/admin';

export default ({ config }: ConfigContext): ExpoConfig => ({
  ...config,
  name: 'Birge Admin',
  slug: 'birge-admin',
  version: '0.1.0',
  scheme: 'birge-admin',
  orientation: 'portrait',
  userInterfaceStyle: 'automatic',
  // Web-only staff console; native identifiers exist only because Expo requires them.
  ios: {
    bundleIdentifier: 'invalid.example.birge.admin',
  },
  android: {
    package: 'invalid.example.birge.admin',
  },
  web: {
    favicon: './assets/brand/favicon.png',
    bundler: 'metro',
    output: 'single',
    name: 'Birge Admin',
    shortName: 'Admin',
  },
  experiments: {
    baseUrl: basePath,
  },
  plugins: ['expo-router', 'expo-localization', 'expo-font', 'expo-web-browser'],
  extra: {
    apiUrl: env.EXPO_PUBLIC_API_URL ?? 'http://localhost:8080/v1',
    staffIssuer: env.EXPO_PUBLIC_STAFF_ISSUER ?? 'http://localhost:4444/',
    staffClientId: env.EXPO_PUBLIC_STAFF_CLIENT_ID ?? 'birge-admin',
    basePath,
  },
});
