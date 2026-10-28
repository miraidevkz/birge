// The child card a mother shared with a trusted contact, shared by the two
// places it is read: the web page of an external contact (`/plan#<token>`,
// viewExternalPlan) and the in-app screen of a member contact
// (`/shared-plan/<ownerId>`, getSharedPlan). Both get the same `SharedPlan`,
// so they show it the same way. No animation of its own: the Card's entrance
// is switched off on the crisis-variant screens that render it.
import type { Schemas } from '@birge/api';
import { i18n, useTranslation } from '@birge/i18n';
import { Card, Heading, Text } from '@birge/ui';

import { formatDateTime } from '@/lib/format';

/** SharedPlanCardProps: the plan as the API returned it. */
export interface SharedPlanCardProps {
  plan: Schemas['SharedPlan'];
}

/** SharedPlanCard shows who shared the plan, the child card and when it was sent. */
export function SharedPlanCard({ plan }: SharedPlanCardProps) {
  const { t } = useTranslation('emergency');
  return (
    <>
      <Text>{t('planCard.intro', { name: plan.ownerDisplayName })}</Text>
      <Card>
        <Heading>{t('planCard.heading')}</Heading>
        <Text>{plan.childCard}</Text>
        <Text variant="caption" tone="muted">
          {t('planCard.sentAt', { when: formatDateTime(plan.sentAt, i18n.language) })}
        </Text>
      </Card>
    </>
  );
}
