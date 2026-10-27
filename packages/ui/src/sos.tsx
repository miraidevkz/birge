// The persistent SOS / help entry (ADR-0014: SOS reachable from every
// screen). The app says where it leads through SosProvider, so this package
// stays free of navigation; Screen renders the button on every screen.
import { useTranslation } from '@birge/i18n';
import { createContext, useContext, type ReactNode } from 'react';

import { Button } from './button';

const SosContext = createContext<(() => void) | null>(null);

/** SosProvider sets what the SOS entry does (the app opens its help screen). */
export function SosProvider({ onPress, children }: { onPress: () => void; children: ReactNode }) {
  return <SosContext.Provider value={onPress}>{children}</SosContext.Provider>;
}

/** SosButton is the SOS entry. It renders nothing outside a SosProvider. */
export function SosButton() {
  const { t } = useTranslation('ui');
  const onPress = useContext(SosContext);
  if (!onPress) return null;
  return (
    <Button
      title={t('sos.label')}
      accessibilityHint={t('sos.hint')}
      onPress={onPress}
      variant="destructive"
      size="sm"
      testID="sos-entry"
    />
  );
}
