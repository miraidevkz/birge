// A small horizontal bar view of a series (weekly or monthly activity in the
// staff console's metrics). Bars are plain views in theme colours: no chart
// library (ADR-0014, ADR-0012) and no animation, so it reads the same under
// reduce motion. The component carries no text of its own; the screen passes
// every label, including the text for a value it may not show.
import { View } from 'react-native';

import { Text } from './text';

/** BarSeriesItem is one bar: a label, the value it is drawn from, and the text shown for it. */
export interface BarSeriesItem {
  /** key identifies the bar (a bucket date); it also names the row's testID. */
  key: string;
  label: string;
  /**
   * value draws the bar against the largest value of the series. Leave it
   * undefined when the value must not be shown (a privacy-suppressed cell):
   * the row then has no bar at all, only valueLabel, so a hidden value never
   * looks like zero.
   */
  value?: number;
  /** valueLabel is the text beside the bar: the formatted number, or why there is none. */
  valueLabel: string;
}

/** BarSeriesProps: a labelled list of bars. */
export interface BarSeriesProps {
  /** label titles the series and prefixes each row's accessibility label. */
  label: string;
  items: readonly BarSeriesItem[];
  testID?: string;
}

/**
 * BarSeries draws one row per item: label and value text on top, a bar below
 * whose length is the item's share of the largest value. Each row is read by
 * screen readers as one element ("series, label: value"); the bars themselves
 * are decorative.
 */
export function BarSeries({ label, items, testID }: BarSeriesProps) {
  const max = items.reduce((m, i) => (i.value !== undefined && i.value > m ? i.value : m), 0);
  return (
    <View className="gap-3" testID={testID}>
      <Text variant="label">{label}</Text>
      {items.map((item) => {
        const shown = item.value !== undefined;
        const share = shown && max > 0 ? Math.max(0, Math.min(1, (item.value ?? 0) / max)) : 0;
        return (
          <View
            key={item.key}
            className="gap-1"
            accessible
            accessibilityLabel={`${label}, ${item.label}: ${item.valueLabel}`}
            testID={testID ? `${testID}-${item.key}` : undefined}
          >
            <View className="flex-row flex-wrap justify-between gap-2">
              <Text variant="caption">{item.label}</Text>
              <Text variant="caption" tone={shown ? 'default' : 'muted'}>
                {item.valueLabel}
              </Text>
            </View>
            {shown ? (
              <View className="h-2 rounded-full bg-muted" testID={testID ? `${testID}-${item.key}-bar` : undefined}>
                {/* The width is data, not a design value: the one style this component sets. */}
                <View className="h-2 rounded-full bg-primary" style={{ width: `${share * 100}%` }} />
              </View>
            ) : null}
          </View>
        );
      })}
    </View>
  );
}
