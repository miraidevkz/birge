// The plan page an external trusted contact opens from the plan SMS
// (`<public URL>/plan#<token>`, contract: viewExternalPlan). The contact has no
// account, so the page stands alone: no session, no Back, no SOS entry, no
// link into the app, nothing that names the app's subject beyond the mother's
// name. It is crisis-adjacent (the contact may be reading it while helping
// with the child), so it has no animation (Screen variant "crisis").
//
// The token lives only in memory for as long as a retry may need it: it is
// not stored, not logged and not part of a React Query key (the request is a
// plain call, not a cached query), so nothing can persist or expose it.
import { ApiError, unwrap, type Schemas } from '@birge/api';
import { i18n, useTranslation } from '@birge/i18n';
import { Card, ErrorState, Heading, LoadingState, Screen, Text } from '@birge/ui';
import { useCallback, useEffect, useRef, useState } from 'react';

import { publicApi } from '@/lib/api';
import { formatDateTime } from '@/lib/format';
import { takePlanToken } from '@/lib/plan-link';

type SharedPlan = Schemas['SharedPlan'];

type PageState =
  | { kind: 'loading' }
  | { kind: 'plan'; plan: SharedPlan }
  | { kind: 'expired' }
  | { kind: 'busy' }
  | { kind: 'offline' };

/**
 * outcome maps a failed request to what the contact sees. Unknown, expired
 * and revoked links all come back as the same 404 (the server cannot tell
 * them apart on purpose), and so does anything else the server refuses.
 * Only a lost connection or a server failure is worth a retry.
 */
function outcome(err: unknown): PageState {
  if (err instanceof ApiError) {
    if (err.status === 429) return { kind: 'busy' };
    if (err.status >= 500) return { kind: 'offline' };
    return { kind: 'expired' };
  }
  return { kind: 'offline' };
}

/** ExternalPlanScreen reads the link token, fetches the shared plan and shows the child card. */
export function ExternalPlanScreen() {
  const { t } = useTranslation('emergency');
  const [state, setState] = useState<PageState>({ kind: 'loading' });
  const token = useRef<string | null>(null);
  const taken = useRef(false);

  const load = useCallback(async () => {
    const value = token.current;
    if (!value) {
      setState({ kind: 'expired' });
      return;
    }
    setState({ kind: 'loading' });
    try {
      const plan = await unwrap(publicApi.POST('/external/plans/view', { body: { token: value } }));
      setState({ kind: 'plan', plan });
    } catch (err) {
      const next = outcome(err);
      setState(next);
      // Only a retry needs the token; otherwise let it go.
      if (next.kind !== 'offline') token.current = null;
      return;
    }
    token.current = null;
  }, []);

  useEffect(() => {
    // Once per page: the fragment is gone after the first read, and a second
    // effect run (React strict mode) must not turn a good link into "expired".
    if (taken.current) return;
    taken.current = true;
    token.current = takePlanToken();
    void load();
  }, [load]);

  const title = t('external.title');
  useEffect(() => {
    // The browser tab shows the same neutral title, not the app's name.
    if (typeof document !== 'undefined') document.title = title;
  }, [title]);

  return (
    <Screen title={title} variant="crisis" testID="external-plan">
      {state.kind === 'loading' ? <LoadingState /> : null}
      {state.kind === 'plan' ? (
        <>
          <Text>{t('external.intro', { name: state.plan.ownerDisplayName })}</Text>
          <Card>
            <Heading>{t('external.cardHeading')}</Heading>
            <Text>{state.plan.childCard}</Text>
            <Text variant="caption" tone="muted">
              {t('external.sentAt', { when: formatDateTime(state.plan.sentAt, i18n.language) })}
            </Text>
          </Card>
        </>
      ) : null}
      {state.kind === 'expired' ? (
        <ErrorState title={t('external.expiredTitle')} description={t('external.expiredDescription')} />
      ) : null}
      {state.kind === 'busy' ? (
        <ErrorState title={t('external.busyTitle')} description={t('external.busyDescription')} />
      ) : null}
      {state.kind === 'offline' ? <ErrorState onRetry={() => void load()} /> : null}
    </Screen>
  );
}
