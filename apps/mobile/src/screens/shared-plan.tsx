// The emergency plan another member sent to her as a confirmed trusted
// contact (contract: getSharedPlan, `GET /trusted/plans/{ownerId}`). Opened
// from the `emergency.plan_sent` inbox item, whose only param is the owner's
// id. It shows the same card as the external contact's web page.
//
// Every read is audited by the server and answered with `no-store`, so the
// card is not kept in the query cache once the screen is gone (gcTime 0) and
// is not refetched on every focus. The server answers 403 without a reason
// for anything that is not allowed (not her contact any more, removed, plan
// not sent); the screen says only that the plan is not available.
//
// Crisis-adjacent (she may read it while helping with the child): no
// animation (Screen variant "crisis").
import { ApiError, unwrap } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { ErrorState, LoadingState, Screen } from '@birge/ui';
import { useQuery } from '@tanstack/react-query';

import { api } from '@/lib/api';
import { isUuid } from '@/lib/notification-targets';

import { SharedPlanCard } from './shared-plan-card';

/** unavailable is a refusal the member cannot fix by retrying. */
function unavailable(err: unknown): boolean {
  return err instanceof ApiError && err.status >= 400 && err.status < 500 && err.status !== 429;
}

/** SharedPlanScreen shows the plan a member sent to her, by the owner's id. */
export function SharedPlanScreen({ ownerId }: { ownerId: string }) {
  const { t } = useTranslation('emergency');
  const valid = isUuid(ownerId);
  const plan = useQuery({
    queryKey: ['emergency', 'shared-plan', ownerId],
    queryFn: () => unwrap(api.GET('/trusted/plans/{ownerId}', { params: { path: { ownerId } } })),
    enabled: valid,
    gcTime: 0,
    staleTime: Infinity,
  });

  const title = t('shared.title');
  let body;
  if (!valid || (plan.isError && unavailable(plan.error))) {
    body = <ErrorState title={t('shared.unavailableTitle')} description={t('shared.unavailableDescription')} />;
  } else if (plan.data) {
    body = <SharedPlanCard plan={plan.data} />;
  } else if (plan.isError) {
    body = <ErrorState description={t('loadError')} onRetry={() => void plan.refetch()} />;
  } else {
    body = <LoadingState />;
  }

  return (
    <Screen title={title} variant="crisis" testID="shared-plan">
      {body}
    </Screen>
  );
}
