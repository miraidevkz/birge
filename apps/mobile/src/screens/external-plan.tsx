// The plan page an external trusted contact opens from the plan SMS
// (`<public URL>/plan#<token>`, contract: viewExternalPlan). The contact has no
// account, so the page stands alone: no session, no Back, no SOS entry, no
// link into the app, nothing that names the app's subject beyond the mother's
// name. It is crisis-adjacent (the contact may be reading it while helping
// with the child), so it has no animation (Screen variant "crisis").
//
// The token is handled by useLinkView: read once from the fragment, kept in
// memory only for a retry, never stored, logged or used as a query key.
import { unwrap } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { ErrorState, LoadingState, Screen } from '@birge/ui';

import { publicApi } from '@/lib/api';
import { useDocumentTitle, useLinkView } from '@/lib/link-view';

import { SharedPlanCard } from './shared-plan-card';

const viewPlan = (token: string) => unwrap(publicApi.POST('/external/plans/view', { body: { token } }));

/** ExternalPlanScreen reads the link token, fetches the shared plan and shows the child card. */
export function ExternalPlanScreen() {
  const { t } = useTranslation('emergency');
  const { state, retry } = useLinkView(viewPlan);
  const title = t('external.title');
  useDocumentTitle(title);

  return (
    <Screen title={title} variant="crisis" testID="external-plan">
      {state.kind === 'loading' ? <LoadingState /> : null}
      {state.kind === 'ok' ? <SharedPlanCard plan={state.data} /> : null}
      {state.kind === 'gone' ? (
        <ErrorState title={t('external.expiredTitle')} description={t('external.expiredDescription')} />
      ) : null}
      {state.kind === 'busy' ? (
        <ErrorState title={t('external.busyTitle')} description={t('external.busyDescription')} />
      ) : null}
      {state.kind === 'offline' ? <ErrorState onRetry={retry} /> : null}
    </Screen>
  );
}
