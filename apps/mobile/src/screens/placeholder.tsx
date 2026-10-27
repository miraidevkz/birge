// A tab whose screens are built in later tasks.
import { useTranslation, type Namespace } from '@birge/i18n';
import { EmptyState, Screen } from '@birge/ui';
import type { ReactNode } from 'react';

/** PlaceholderScreen shows the tab title and a "coming soon" state. */
export function PlaceholderScreen({ ns, children }: { ns: Namespace; children?: ReactNode }) {
  const { t } = useTranslation(ns);
  const { t: tCommon } = useTranslation('common');
  return (
    <Screen title={t('title')}>
      <EmptyState title={tCommon('comingSoon.title')} description={tCommon('comingSoon.description')} />
      {children}
    </Screen>
  );
}
