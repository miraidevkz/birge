// About me: her district and her children (birth year and needs). Rooms, tea
// matches and specialists all depend on them; before this screen no member
// could set them, so the Circle stayed empty. Children carry no names (schema).
import { unwrap } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import {
  Banner,
  Button,
  Card,
  CheckItem,
  ErrorState,
  Heading,
  ListItem,
  LoadingState,
  Screen,
  Text,
  TextField,
} from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

import { api } from '@/lib/api';
import { cityLabel, districtLabel, needLabel, useDistricts, useNeeds } from '@/lib/reference';

const YEAR = /^\d{4}$/;

/** AboutMeScreen edits the member's district and children. */
export function AboutMeScreen() {
  const { t } = useTranslation('about');
  const client = useQueryClient();
  const me = useQuery({ queryKey: ['me'], queryFn: () => unwrap(api.GET('/me')) });
  const districts = useDistricts();
  const needs = useNeeds();
  const [status, setStatus] = useState<'idle' | 'saved' | 'failed'>('idle');
  const [year, setYear] = useState('');
  const [picked, setPicked] = useState<string[]>([]);
  const [yearError, setYearError] = useState(false);

  const saveDistrict = useMutation({
    mutationFn: (districtId: string) => unwrap(api.PATCH('/me', { body: { districtId } })),
    onSuccess: (member) => {
      client.setQueryData(['me'], member);
      // Rooms and matches depend on the district.
      void client.invalidateQueries({ queryKey: ['rooms'] });
      setStatus('saved');
    },
    onError: () => setStatus('failed'),
  });

  const addChild = useMutation({
    mutationFn: (body: { birthYear: number; needs: string[] }) => unwrap(api.POST('/me/children', { body })),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['me'] });
      setYear('');
      setPicked([]);
      setStatus('idle');
    },
    onError: () => setStatus('failed'),
  });

  const removeChild = useMutation({
    mutationFn: (childId: string) => unwrap(api.DELETE('/me/children/{childId}', { params: { path: { childId } } })),
    onSuccess: () => void client.invalidateQueries({ queryKey: ['me'] }),
    onError: () => setStatus('failed'),
  });

  if (me.isLoading) {
    return (
      <Screen title={t('title')}>
        <LoadingState />
      </Screen>
    );
  }
  if (!me.data) {
    return (
      <Screen title={t('title')}>
        <ErrorState onRetry={() => void me.refetch()} />
      </Screen>
    );
  }
  const member = me.data;

  function onAdd() {
    const y = Number(year);
    if (!YEAR.test(year) || y < 1990 || y > new Date().getFullYear()) {
      setYearError(true);
      return;
    }
    setYearError(false);
    addChild.mutate({ birthYear: y, needs: picked });
  }

  return (
    <Screen title={t('title')} testID="about-me">
      <Text tone="muted">{t('intro')}</Text>
      {status === 'saved' ? <Banner tone="success">{t('district.saved')}</Banner> : null}
      {status === 'failed' ? <Banner tone="danger">{t('failed')}</Banner> : null}

      <Card testID="about-district">
        <Heading>{t('district.heading')}</Heading>
        {districts.data && districts.data.length > 0 ? (
          districts.data.map((city) => (
            <Card key={city.id}>
              <Text variant="label" tone="muted">
                {cityLabel(city.code)}
              </Text>
              {city.districts.map((d) => (
                <CheckItem
                  key={d.id}
                  radio
                  title={districtLabel(d.code)}
                  checked={member.districtId === d.id}
                  disabled={saveDistrict.isPending}
                  onChange={() => saveDistrict.mutate(d.id)}
                  testID={`about-district-${d.code}`}
                />
              ))}
            </Card>
          ))
        ) : (
          <Text tone="muted">{districts.isLoading ? t('ui:loading') : t('district.none')}</Text>
        )}
      </Card>

      <Card testID="about-children">
        <Heading>{t('children.heading')}</Heading>
        {member.children.length === 0 ? <Text tone="muted">{t('children.none')}</Text> : null}
        {member.children.map((c) => (
          <ListItem
            key={c.id}
            title={t('children.item', { year: c.birthYear })}
            subtitle={c.needs.length > 0 ? c.needs.map(needLabel).join(', ') : t('children.noNeeds')}
            trailing={
              <Button
                title={t('children.remove')}
                variant="secondary"
                size="sm"
                loading={removeChild.isPending && removeChild.variables === c.id}
                onPress={() => removeChild.mutate(c.id)}
                testID={`about-child-remove-${c.id}`}
              />
            }
          />
        ))}
        <Text variant="label">{t('children.add')}</Text>
        <TextField
          label={t('children.year')}
          hint={t('children.yearHint')}
          error={yearError ? t('children.invalidYear') : undefined}
          value={year}
          onChangeText={setYear}
          keyboardType="number-pad"
          maxLength={4}
          testID="about-child-year"
        />
        {needs.data && needs.data.length > 0 ? (
          <>
            <Text variant="label" tone="muted">
              {t('children.needs')}
            </Text>
            {needs.data.map((code) => (
              <CheckItem
                key={code}
                title={needLabel(code)}
                checked={picked.includes(code)}
                onChange={(on) => setPicked(on ? [...picked, code] : picked.filter((p) => p !== code))}
                testID={`about-need-${code}`}
              />
            ))}
          </>
        ) : null}
        <Button title={t('children.save')} onPress={onAdd} loading={addChild.isPending} testID="about-child-save" />
      </Card>
    </Screen>
  );
}
