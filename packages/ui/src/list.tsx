import { useTranslation } from '@birge/i18n';
import { FlashList, type ListRenderItem } from '@shopify/flash-list';
import { useCallback, type ReactElement } from 'react';
import { View } from 'react-native';

import { Appear, AppearListScope } from './appear';
import { Banner } from './banner';
import { Button } from './button';
import { EmptyState, ErrorState, LoadingState, type EmptyStateProps } from './states';
import { tokens } from './theme';

/** ListProps: data plus the state of its query; the list picks the right state to show. */
export interface ListProps<T> {
  /** data is undefined until the first load finished. */
  data: readonly T[] | undefined;
  renderItem: ListRenderItem<T>;
  keyExtractor: (item: T) => string;
  /** loading is the first load; a refresh with data on screen is not loading. */
  loading?: boolean;
  /** error is the last load error; with data on screen it shows as a banner above the rows. */
  error?: unknown;
  onRetry?: () => void;
  empty: EmptyStateProps;
  /** header scrolls with the rows (screen intro, summary cards). */
  header?: ReactElement;
  /** footer scrolls after the rows (a secondary form); keep the Screen footer for the one primary action. */
  footer?: ReactElement;
  testID?: string;
}

/**
 * List is the only scrolling list component (FlashList, ADR-0014). It owns
 * the four states of a data screen: loading, error without data, empty, and
 * content (with a non-blocking error when a refresh failed). The first rows
 * fade in, slightly staggered, when the data first shows (Appear); rows that
 * mount later while she scrolls, and re-rendered rows, appear without motion.
 */
export function List<T>({
  data,
  renderItem,
  keyExtractor,
  loading,
  error,
  onRetry,
  empty,
  header,
  footer,
  testID,
}: ListProps<T>) {
  const { t } = useTranslation('ui');
  // Every row is wrapped once here, so a screen never animates rows itself.
  const renderRow = useCallback<ListRenderItem<T>>(
    (info) => <Appear index={info.index}>{renderItem(info)}</Appear>,
    [renderItem],
  );
  let body: ReactElement | null = null;
  if (data === undefined) {
    body = error ? <ErrorState {...(onRetry ? { onRetry } : {})} /> : loading ? <LoadingState /> : null;
  }
  const staleError =
    data !== undefined && error ? (
      <Banner
        tone="warning"
        action={
          onRetry ? <Button title={t('error.retry')} onPress={onRetry} variant="secondary" size="sm" /> : undefined
        }
      >
        {t('error.description')}
      </Banner>
    ) : null;
  // The list scrolls the whole screen width, so its scrollbar sits at the
  // screen edge: it cancels the screen's 20 px gutter (Screen, px-5) and
  // pads its own content instead. A List always lives directly in a Screen.
  return (
    <View className="-mx-5 flex-1">
      <AppearListScope>
        <FlashList
          testID={testID}
          data={data ?? []}
          renderItem={renderRow}
          keyExtractor={keyExtractor}
          ListHeaderComponent={
            <View className="gap-4 pb-2">
              {header}
              {staleError}
              {body}
            </View>
          }
          ListEmptyComponent={data !== undefined ? <EmptyState {...empty} /> : null}
          ListFooterComponent={footer ? <View className="gap-4 pt-4">{footer}</View> : undefined}
          contentContainerStyle={{ paddingBottom: tokens.spacing[6], paddingHorizontal: tokens.spacing[5] }}
        />
      </AppearListScope>
    </View>
  );
}
