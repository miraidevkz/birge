// Runtime configuration, from app.config.ts `extra` (filled from env at build
// time). Validated once at startup so a misconfigured build fails at the
// first launch in testing, not halfway through a crisis call.
import type { AppPlatform, Schemas } from '@birge/api';
import Constants from 'expo-constants';
import { Platform } from 'react-native';

/** CrisisPhone is one crisis line as the API returns it. */
export type CrisisPhone = Schemas['CrisisPhone'];

/** AppConfig is the validated configuration. */
export interface AppConfig {
  apiUrl: string;
  kratosUrl: string;
  /** crisisPhones is the build-time fallback list, used until the server has answered once. */
  crisisPhones: CrisisPhone[];
  /**
   * demoCodesUrl is set only in demo builds: the dev stack's endpoint that
   * returns the sign-in code of a synthetic demo number while no SMS provider
   * is connected. Undefined everywhere else.
   */
  demoCodesUrl?: string;
  /**
   * androidPush is true when the Android build carries a Firebase config
   * (BIRGE_GOOGLE_SERVICES_FILE). Without it there is no FCM token, so the
   * app never asks for the notification permission on Android.
   */
  androidPush: boolean;
  appVersion: string;
  platform: AppPlatform;
}

/** PHONE matches the contract's CrisisPhone.number; anything else never reaches a tel: link. */
const PHONE = /^[0-9+]{2,16}$/;

/** isCrisisPhone validates untrusted data (storage, server, build env) before it is shown or dialled. */
export function isCrisisPhone(x: unknown): x is CrisisPhone {
  if (x === null || typeof x !== 'object') return false;
  const p = x as Record<string, unknown>;
  return typeof p.number === 'string' && PHONE.test(p.number) && typeof p.label === 'string' && p.label.length <= 120;
}

function url(name: string, value: unknown): string {
  if (typeof value !== 'string') throw new Error(`config: ${name} is missing`);
  const u = new URL(value);
  if (u.protocol !== 'https:' && u.protocol !== 'http:') throw new Error(`config: ${name} must be http(s)`);
  return value.replace(/\/+$/, '');
}

function phones(raw: unknown): CrisisPhone[] {
  if (typeof raw !== 'string') return [];
  try {
    const parsed: unknown = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter(isCrisisPhone).slice(0, 10) : [];
  } catch {
    return [];
  }
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
    kratosUrl: url('kratosUrl', extra?.kratosUrl),
    crisisPhones: phones(extra?.crisisPhones),
    demoCodesUrl:
      typeof extra?.demoCodesUrl === 'string' && extra.demoCodesUrl !== ''
        ? url('demoCodesUrl', extra.demoCodesUrl)
        : undefined,
    androidPush: extra?.androidPush === '1',
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
