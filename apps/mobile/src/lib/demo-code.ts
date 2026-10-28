// Demo builds only: reads the sign-in code of a synthetic demo number from the
// dev stack (cmd/birge-devsms, GET /demo/code) so a showcase works while no
// SMS provider is connected. The server answers only for the demo range, so a
// real number's code is never shown. Off unless the build sets
// EXPO_PUBLIC_DEMO_CODES_URL.
import { useEffect, useState } from 'react';

import { config } from './config';

/** DEMO_POLL_MS and DEMO_POLL_TRIES bound the wait for the courier to deliver. */
export const DEMO_POLL_MS = 1500;
export const DEMO_POLL_TRIES = 20;

/** CODE is what a Kratos code looks like; anything else is ignored. */
const CODE = /^\d{4,8}$/;

/** fetchDemoCode asks once; null when there is no code (yet) or on any error. */
export async function fetchDemoCode(baseUrl: string, phone: string): Promise<string | null> {
  try {
    const res = await fetch(`${baseUrl}?phone=${encodeURIComponent(phone)}`, {
      headers: { Accept: 'application/json' },
    });
    if (!res.ok) return null;
    const body: unknown = await res.json();
    const code = body && typeof body === 'object' ? (body as { code?: unknown }).code : undefined;
    return typeof code === 'string' && CODE.test(code) ? code : null;
  } catch {
    return null;
  }
}

/**
 * useDemoCode polls for the demo code of phone while active. Returns
 * undefined when demo codes are off, null while waiting, the code once known.
 */
export function useDemoCode(phone: string | null, sentAt: number): string | null | undefined {
  const baseUrl = config.demoCodesUrl;
  const key = `${phone ?? ''}|${sentAt}`;
  // The answer is stored with the request it belongs to, so a code for an
  // earlier number or send is never shown for the current one.
  const [found, setFound] = useState<{ key: string; code: string } | null>(null);

  useEffect(() => {
    if (!baseUrl || !phone) return;
    let stopped = false;
    let tries = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = async () => {
      const c = await fetchDemoCode(baseUrl, phone);
      if (stopped) return;
      if (c) {
        setFound({ key, code: c });
        return;
      }
      tries += 1;
      if (tries < DEMO_POLL_TRIES) timer = setTimeout(() => void tick(), DEMO_POLL_MS);
    };
    void tick();
    return () => {
      stopped = true;
      if (timer) clearTimeout(timer);
    };
  }, [baseUrl, phone, key]);

  if (!baseUrl) return undefined;
  return found?.key === key ? found.code : null;
}
