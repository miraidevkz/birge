import { useTranslation } from '@birge/i18n';
import { useId, useMemo, useState } from 'react';
import { ScrollView, View } from 'react-native';
import Svg, { Path } from 'react-native-svg';

import { CheckItem } from './check-item';
import { cn } from './cn';
import { PressableBox } from './pressable-box';
import { Sheet } from './sheet';
import { Text } from './text';
import { TextField } from './text-field';
import { useThemeColors } from './theme';

/** SelectOption is one choice of a SelectField. */
export interface SelectOption {
  value: string;
  label: string;
}

/** SelectSection is a titled group of options (the districts of one city); title may be empty. */
export interface SelectSection {
  title: string;
  options: SelectOption[];
}

/** SelectFieldProps: a labelled field that picks one option from a long, grouped list. */
export interface SelectFieldProps {
  label: string;
  sections: SelectSection[];
  /** value is the selected option's value; undefined or unknown shows the placeholder. */
  value: string | undefined;
  onChange: (value: string) => void;
  /** placeholder is shown when nothing is selected. */
  placeholder: string;
  /** searchable adds a filter box over the list (from about a dozen options). */
  searchable?: boolean;
  disabled?: boolean;
  /** testID names the field; options get `${testID}-${value}`, the filter `${testID}-search`. */
  testID?: string;
}

/**
 * SelectField replaces a long list of radios or chips (36 districts in 20
 * cities) with one field: it shows the current choice and opens a Sheet with
 * the grouped options, optionally filtered by a search box. A section title
 * that matches the search keeps all its options, so "Алматы" finds every
 * Almaty district. The options are CheckItem radios, announced with state.
 */
export function SelectField({
  label,
  sections,
  value,
  onChange,
  placeholder,
  searchable = false,
  disabled = false,
  testID,
}: SelectFieldProps) {
  const { t } = useTranslation('ui');
  const colors = useThemeColors();
  const id = useId();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const current = useMemo(() => {
    for (const s of sections) {
      const o = s.options.find((x) => x.value === value);
      if (o) return s.title && s.title !== o.label ? `${o.label}, ${s.title}` : o.label;
    }
    return undefined;
  }, [sections, value]);
  const shown = useMemo(() => {
    const q = query.trim().toLocaleLowerCase();
    if (q === '') return sections;
    return sections
      .map((s) =>
        s.title.toLocaleLowerCase().includes(q)
          ? s
          : { ...s, options: s.options.filter((o) => o.label.toLocaleLowerCase().includes(q)) },
      )
      .filter((s) => s.options.length > 0);
  }, [sections, query]);
  const close = () => {
    setOpen(false);
    setQuery('');
  };
  return (
    <View className="gap-1">
      <Text variant="label" nativeID={`${id}-label`}>
        {label}
      </Text>
      <PressableBox
        accessibilityRole="button"
        accessibilityLabel={`${label}: ${current ?? placeholder}`}
        accessibilityState={{ disabled, expanded: open }}
        disabled={disabled}
        onPress={() => setOpen(true)}
        testID={testID}
        boxClassName={cn(
          'min-h-touch flex-row items-center gap-3 rounded-md border border-border bg-card px-4 py-2',
          disabled && 'opacity-50',
        )}
      >
        <Text tone={current ? 'default' : 'muted'} className="flex-1">
          {current ?? placeholder}
        </Text>
        <Svg width={20} height={20} viewBox="0 0 24 24" accessibilityElementsHidden importantForAccessibility="no">
          <Path d="M6 9l6 6 6-6" stroke={colors.mutedForeground} strokeWidth={2} fill="none" strokeLinecap="round" />
        </Svg>
      </PressableBox>
      <Sheet visible={open} onClose={close} title={label} testID={testID ? `${testID}-sheet` : undefined}>
        {searchable ? (
          <TextField
            label={t('select.search')}
            value={query}
            onChangeText={setQuery}
            autoCorrect={false}
            testID={testID ? `${testID}-search` : undefined}
          />
        ) : null}
        <ScrollView className="max-h-sheet" keyboardShouldPersistTaps="handled">
          {shown.length === 0 ? <Text tone="muted">{t('select.nothing')}</Text> : null}
          {shown.map((s) => (
            <View key={s.title || '-'} className="gap-1 pb-3">
              {s.title ? (
                <Text variant="label" tone="muted">
                  {s.title}
                </Text>
              ) : null}
              {s.options.map((o) => (
                <CheckItem
                  key={o.value}
                  radio
                  title={o.label}
                  checked={o.value === value}
                  onChange={() => {
                    onChange(o.value);
                    close();
                  }}
                  testID={testID ? `${testID}-${o.value}` : undefined}
                />
              ))}
            </View>
          ))}
        </ScrollView>
      </Sheet>
    </View>
  );
}
