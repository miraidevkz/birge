// Runtime configuration, from app.config.ts `extra` (filled from env at build
// time). Validated once at startup so a misconfigured build fails early.
import type { AppPlatform } from '@birge/api';
import Constants from 'expo-constants';
import { Platform } from 'react-native';

/** AppConfig is the validated configuration of this admin build. */
export interface AppConfig {
  apiUrl: string;
  /** staffIssuer is the OIDC issuer URL (discovery document base). */
  staffIssuer: string;
  /** staffClientId is the public OIDC client id for PKCE. */
  staffClientId: string;
  appVersion: string;
  platform: AppPlatform;
}

function url(name: string, value: unknown): string {
  if (typeof value !== 'string') throw new Error(`config: ${name} is missing`);
  const u = new URL(value);
  if (u.protocol !== 'https:' && u.protocol !== 'http:') throw new Error(`config: ${name} must be http(s)`);
  return value.replace(/\/+$/, '');
}

function nonEmpty(name: string, value: unknown): string {
  if (typeof value !== 'string' || value.trim() === '') throw new Error(`config: ${name} is missing`);
  return value.trim();
}

/** parseConfig validates the embedded configuration. */
export function parseConfig(
  extra: Record<string, unknown> | undefined,
  version: string | undefined,
  os: string,
): AppConfig {
  const platform: AppPlatform = os === 'ios' || os === 'android' ? os : 'web';
  return {
    apiUrl: url('apiUrl', extra?.apiUrl),
    staffIssuer: url('staffIssuer', extra?.staffIssuer),
    staffClientId: nonEmpty('staffClientId', extra?.staffClientId),
    appVersion: version && /^\d+\.\d+\.\d+$/.test(version) ? version : '0.0.0',
    platform,
  };
}

/** config is the configuration of this build. */
export const config: AppConfig = parseConfig(
  Constants.expoConfig?.extra as Record<string, unknown> | undefined,
  Constants.expoConfig?.version,
  Platform.OS,
);
