// The SOS page an external trusted contact opens from the SOS SMS
// (`<public URL>/sos-link#<token>`, contract: viewExternalSos). Like the plan
// page it stands alone: no session, no Back, no SOS entry of its own, no
// link into the app. It is a crisis screen: no animation (Screen variant
// "crisis") and the same "call 112" banner as the in-app SOS screens, first,
// before anything is loaded, so it works even when the request does not.
//
// Every type is shown plainly, childcare included: an external trusted
// contact is one of the people the mother chose (AGENTS.md non-negotiable 5).
// The contact has no account, so there is no "I can help" here; the page asks
// her to get in touch the way she usually does, and never shows or asks for
// a phone number. A closed request and an expired link are the same 404 and
// get the same calm text.
//
// The token is handled by useLinkView: read once from the fragment, kept in
// memory only for a retry, never stored, logged or used as a query key.
import { unwrap, type Schemas } from '@birge/api';
import { i18n, useTranslation } from '@birge/i18n';
import { Card, ErrorState, Heading, LifeThreatBanner, LoadingState, Screen, Text } from '@birge/ui';

import { publicApi } from '@/lib/api';
import { formatDateTime } from '@/lib/format';
import { useDocumentTitle, useLinkView } from '@/lib/link-view';

type ExternalSos = Schemas['ExternalSos'];

const viewSos = (token: string) => unwrap(publicApi.POST('/external/sos/view', { body: { token } }));

/** ExternalSosScreen reads the link token, fetches the SOS and shows who asks, what for and when. */
export function ExternalSosScreen() {
  const { t } = useTranslation('emergency');
  const { t: tHelp } = useTranslation('help');
  const { state, retry } = useLinkView(viewSos);
  const title = t('externalSos.title');
  useDocumentTitle(title);

  return (
    <Screen title={title} variant="crisis" testID="external-sos">
      <LifeThreatBanner message={tHelp('lifeThreat')} callTitle={tHelp('call112')} />
      {state.kind === 'loading' ? <LoadingState /> : null}
      {state.kind === 'ok' ? <SosDetails sos={state.data} /> : null}
      {state.kind === 'gone' ? (
        <ErrorState title={t('externalSos.goneTitle')} description={t('externalSos.goneDescription')} />
      ) : null}
      {state.kind === 'busy' ? (
        <ErrorState title={t('external.busyTitle')} description={t('external.busyDescription')} />
      ) : null}
      {state.kind === 'offline' ? <ErrorState onRetry={retry} /> : null}
    </Screen>
  );
}

/** SosDetails is the loaded SOS: her first name, the type, the text and the times. */
function SosDetails({ sos }: { sos: ExternalSos }) {
  const { t } = useTranslation('emergency');
  const lang = i18n.language;
  return (
    <>
      <Text>{t('trustedSos.intro', { name: sos.ownerDisplayName })}</Text>
      <Card>
        <Heading>{t(`trustedSos.type.${sos.type}`)}</Heading>
        {sos.type === 'childcare' ? <Text>{t('trustedSos.childcareNote')}</Text> : null}
        <Text>{sos.text}</Text>
        <Text variant="caption" tone="muted">
          {t('trustedSos.raisedAt', { when: formatDateTime(sos.createdAt, lang) })}
        </Text>
        {sos.closesAt ? (
          <Text variant="caption" tone="muted">
            {t('trustedSos.closesAt', { when: formatDateTime(sos.closesAt, lang) })}
          </Text>
        ) : null}
      </Card>
      <Text>{t('externalSos.contactHint')}</Text>
    </>
  );
}
