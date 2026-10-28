// An SOS raised by a member whose confirmed trusted contact she is (contract:
// getTrustedSos, `GET /trusted/sos/{sosId}`; respondAsTrustedContact,
// `POST /trusted/sos/{sosId}/respond`). Opened from the
// `emergency.sos_raised_contact` inbox item, whose only param is the SOS id.
//
// Every SOS type is shown, childcare included, and plainly: a confirmed
// trusted contact is exactly who may help with her child (AGENTS.md
// non-negotiable 5, ADR-0023 addendum 2026-10-28). The screen never suggests
// anyone else for the child and never shows or asks for a phone number
// (requirements: contacts are not disclosed); "I can help" lets the author
// open a private chat with her, as for any response.
//
// Every read is audited by the server and answered with `no-store`, so the
// text is not kept in the query cache once the screen is gone (gcTime 0) and
// is never refetched on focus or reconnect. After "I can help" the screen
// does not read the SOS again (another audited read); it remembers the answer
// locally. The server answers 403 (not her contact) and 404 (unknown) without
// a reason; both say only that the request is not available. A closed SOS
// (status, or 409 on respond) gets a calm text and no action.
//
// Crisis screen: no animation (Screen variant "crisis") and the same "call
// 112" banner as the mom's SOS screen, from the help locale.
import { ApiError, unwrap, type Schemas } from '@birge/api';
import { i18n, useTranslation } from '@birge/i18n';
import { Banner, Button, Card, ErrorState, Heading, LifeThreatBanner, LoadingState, Screen, Text } from '@birge/ui';
import { useMutation, useQuery } from '@tanstack/react-query';

import { api } from '@/lib/api';
import { formatDateTime } from '@/lib/format';
import { isUuid } from '@/lib/notification-targets';

type TrustedSos = Schemas['TrustedSos'];

/** unavailable is a refusal (403, 404) that retrying cannot change. */
function unavailable(err: unknown): boolean {
  return err instanceof ApiError && (err.status === 403 || err.status === 404);
}

/** closedByServer is respond's 409: the SOS was closed meanwhile. */
function closedByServer(err: unknown): boolean {
  return err instanceof ApiError && err.status === 409;
}

/** trustedSosKey is the query key of one SOS read as a trusted contact. */
export const trustedSosKey = (sosId: string) => ['emergency', 'trusted-sos', sosId] as const;

/** TrustedSosScreen shows another member's SOS to her trusted contact and lets her answer "I can help". */
export function TrustedSosScreen({ sosId }: { sosId: string }) {
  const { t } = useTranslation('emergency');
  const { t: tHelp } = useTranslation('help');
  const valid = isUuid(sosId);
  const sos = useQuery({
    queryKey: trustedSosKey(sosId),
    queryFn: () => unwrap(api.GET('/trusted/sos/{sosId}', { params: { path: { sosId } } })),
    enabled: valid,
    gcTime: 0,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  const respond = useMutation({
    mutationFn: () => unwrap(api.POST('/trusted/sos/{sosId}/respond', { params: { path: { sosId } } })),
  });

  let body;
  if (!valid || (sos.isError && unavailable(sos.error)) || unavailable(respond.error)) {
    body = <ErrorState title={t('trustedSos.unavailableTitle')} description={t('trustedSos.unavailableDescription')} />;
  } else if (sos.data) {
    body = (
      <Details
        sos={sos.data}
        respondedNow={respond.isSuccess}
        closedNow={closedByServer(respond.error)}
        failed={respond.isError && !closedByServer(respond.error)}
        pending={respond.isPending}
        onRespond={() => respond.mutate()}
      />
    );
  } else if (sos.isError) {
    body = <ErrorState description={t('trustedSos.loadError')} onRetry={() => void sos.refetch()} />;
  } else {
    body = <LoadingState />;
  }

  return (
    <Screen title={t('trustedSos.title')} variant="crisis" testID="trusted-sos">
      <LifeThreatBanner message={tHelp('lifeThreat')} callTitle={tHelp('call112')} />
      {body}
    </Screen>
  );
}

interface DetailsProps {
  sos: TrustedSos;
  /** respondedNow: "I can help" succeeded on this screen (the SOS is not read again). */
  respondedNow: boolean;
  /** closedNow: respond answered 409, the SOS was closed meanwhile. */
  closedNow: boolean;
  failed: boolean;
  pending: boolean;
  onRespond: () => void;
}

/** Details is the loaded SOS: who asks, what for, when, and her answer. */
function Details({ sos, respondedNow, closedNow, failed, pending, onRespond }: DetailsProps) {
  const { t } = useTranslation('emergency');
  const lang = i18n.language;
  const closed = sos.status === 'closed' || closedNow;
  const responded = sos.respondedByMe || respondedNow;

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
        <Text variant="caption" tone="muted">
          {t(closed ? 'trustedSos.status.closed' : 'trustedSos.status.open')}
        </Text>
        {!closed && sos.closesAt ? (
          <Text variant="caption" tone="muted">
            {t('trustedSos.closesAt', { when: formatDateTime(sos.closesAt, lang) })}
          </Text>
        ) : null}
      </Card>
      {responded ? (
        <>
          <Heading>{t('trustedSos.responded')}</Heading>
          {closed ? null : <Text>{t('trustedSos.respondedDescription')}</Text>}
        </>
      ) : null}
      {closed ? (
        <>
          <Heading>{t('trustedSos.closedTitle')}</Heading>
          <Text tone="muted">{t('trustedSos.closedDescription')}</Text>
        </>
      ) : null}
      {!closed && !responded ? (
        <>
          <Text tone="muted">{t('trustedSos.respondHint')}</Text>
          {failed ? <Banner tone="danger">{t('trustedSos.failed')}</Banner> : null}
          <Button title={t('trustedSos.respond')} loading={pending} onPress={onRespond} testID="trusted-sos-respond" />
        </>
      ) : null}
    </>
  );
}
