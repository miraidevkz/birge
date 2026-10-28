import { languages, useTranslation, type Language } from '@birge/i18n';

import { ChoiceGroup } from './choice-group';

/** LanguagePickerProps: the current language and what to do on a choice. */
export interface LanguagePickerProps {
  value: Language;
  onChange: (lang: Language) => void;
  disabled?: boolean;
  testID?: string;
}

/**
 * LanguagePicker offers the interface languages, each written in itself
 * ("Қазақша", "Русский", "English"), so a member who cannot read the current
 * language still finds hers. Used before sign-in and in the profile.
 */
export function LanguagePicker({ value, onChange, disabled, testID }: LanguagePickerProps) {
  const { t } = useTranslation('common');
  return (
    <ChoiceGroup
      label={t('language.label')}
      value={value}
      onChange={onChange}
      disabled={disabled}
      options={languages.map((code) => ({ value: code, label: t(`language.native.${code}`) }))}
      testID={testID}
    />
  );
}
