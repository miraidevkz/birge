// Shared helpers for admin tests. Not bundled: nothing in src/app imports it.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

import { SessionProvider } from '@/lib/session';

/** Providers wraps a screen with a fresh query client and the session. */
export function Providers({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={client}>
      <SessionProvider>{children}</SessionProvider>
    </QueryClientProvider>
  );
}

/** json builds a fetch Response with a JSON body, or an empty one when body is undefined. */
export function json(status: number, body?: unknown): Response {
  if (body === undefined) return new Response(null, { status });
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

/** Route answers one API call: method (GET by default) and the end of the URL path. */
export interface Route {
  method?: string;
  path: string;
  respond: (req: Request) => Response | Promise<Response>;
}

/**
 * router returns a fetch implementation that answers by method and path, so a
 * screen with several queries does not depend on the order they start in.
 * An unknown call gets a 404 problem.
 */
export function router(routes: Route[]): (input: RequestInfo | URL, init?: RequestInit) => Promise<Response> {
  return async (input, init) => {
    const req = input instanceof Request ? input : new Request(input, init);
    const path = new URL(req.url).pathname;
    const route = routes.find((r) => (r.method ?? 'GET') === req.method && path.endsWith(r.path));
    return route ? route.respond(req) : json(404, { title: 'not found', code: 'not_found' });
  };
}
