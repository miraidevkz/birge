// Motion policy tests (ADR-0014): every animation of packages/ui is decided by
// these hooks, so proving them static under reduce motion and inside NoMotion
// (crisis screens) proves the components static there.
import { afterEach, beforeEach, describe, expect, it, jest } from '@jest/globals';
import { act, render, renderHook, screen, waitFor } from '@testing-library/react-native';
import type { ReactNode } from 'react';
import { AccessibilityInfo } from 'react-native';

import { Appear, AppearListScope, useAppearAnimation } from './appear';
import { Card } from './card';
import { NoMotion, useMotionAllowed } from './motion';
import { usePressFeedback } from './pressable-box';
import { Screen } from './screen';
import { SosProvider } from './sos';
import { Text } from './text';

// Each test states the OS answer itself: restoring spies also resets the
// React Native mocks of AccessibilityInfo, which would leave it unknown.
beforeEach(() => {
  jest.spyOn(AccessibilityInfo, 'isReduceMotionEnabled').mockResolvedValue(false);
  jest
    .spyOn(AccessibilityInfo, 'addEventListener')
    .mockReturnValue({ remove: () => undefined } as ReturnType<typeof AccessibilityInfo.addEventListener>);
});

afterEach(() => {
  jest.restoreAllMocks();
});

function reduceMotionOn() {
  jest.spyOn(AccessibilityInfo, 'isReduceMotionEnabled').mockResolvedValue(true);
}

const noMotion = ({ children }: { children: ReactNode }) => <NoMotion>{children}</NoMotion>;

describe('useMotionAllowed', () => {
  it('is true once the OS says reduce motion is off', async () => {
    const { result } = await renderHook(() => useMotionAllowed());
    await waitFor(() => expect(result.current).toBe(true));
  });

  it('is false while the OS setting is not known yet', async () => {
    jest.spyOn(AccessibilityInfo, 'isReduceMotionEnabled').mockReturnValue(new Promise<boolean>(() => undefined));
    const { result } = await renderHook(() => useMotionAllowed());
    expect(result.current).toBe(false);
  });

  it('is false under reduce motion', async () => {
    reduceMotionOn();
    const { result } = await renderHook(() => useMotionAllowed());
    await act(async () => undefined);
    expect(result.current).toBe(false);
  });

  it('follows the OS setting when it changes', async () => {
    let changed: ((v: boolean) => void) | undefined;
    jest.spyOn(AccessibilityInfo, 'addEventListener').mockImplementation(((_: string, cb: (v: boolean) => void) => {
      changed = cb;
      return { remove: () => undefined };
    }) as unknown as typeof AccessibilityInfo.addEventListener);
    const { result } = await renderHook(() => useMotionAllowed());
    await waitFor(() => expect(result.current).toBe(true));
    await act(async () => changed?.(true));
    expect(result.current).toBe(false);
  });

  it('is false inside NoMotion even with reduce motion off', async () => {
    const { result } = await renderHook(() => useMotionAllowed(), { wrapper: noMotion });
    await act(async () => undefined);
    expect(result.current).toBe(false);
  });

  it('is false everywhere inside a crisis Screen', async () => {
    const seen: boolean[] = [];
    function Probe() {
      seen.push(useMotionAllowed());
      return <Text>probe</Text>;
    }
    await render(
      <SosProvider onPress={() => undefined}>
        <Screen title="Help now" variant="crisis" transitionKey="step">
          <Probe />
        </Screen>
      </SosProvider>,
    );
    await act(async () => undefined);
    expect(seen.length).toBeGreaterThan(0);
    expect(seen.every((v) => v === false)).toBe(true);
  });
});

// MotionWarm keeps the reduce-motion store subscribed and known, so content
// mounted after it decides with the real setting (the app is in this state
// after its first frame).
function MotionWarm({ onMotion }: { onMotion: (v: boolean) => void }) {
  onMotion(useMotionAllowed());
  return null;
}

describe('Appear', () => {
  it('animates on mount when motion is allowed', async () => {
    let motion = false;
    const seen: unknown[] = [];
    function Probe() {
      seen.push(useAppearAnimation());
      return null;
    }
    const tree = (probe: boolean) => (
      <>
        <MotionWarm onMotion={(v) => (motion = v)} />
        {probe ? <Probe /> : null}
      </>
    );
    const { rerender } = await render(tree(false));
    await waitFor(() => expect(motion).toBe(true));
    await rerender(tree(true));
    expect(seen.at(-1)).toBeDefined();
  });

  it('is static under reduce motion', async () => {
    const query = jest.spyOn(AccessibilityInfo, 'isReduceMotionEnabled').mockResolvedValue(true);
    const seen: unknown[] = [];
    function Probe() {
      seen.push(useAppearAnimation());
      return null;
    }
    const tree = (probe: boolean) => (
      <>
        <MotionWarm onMotion={() => undefined} />
        {probe ? <Probe /> : null}
      </>
    );
    const { rerender } = await render(tree(false));
    await waitFor(() => expect(query).toHaveBeenCalled());
    await act(async () => undefined);
    await rerender(tree(true));
    expect(seen).toHaveLength(1);
    expect(seen[0]).toBeUndefined();
  });

  it('is static inside NoMotion', async () => {
    const { result } = await renderHook(() => useAppearAnimation(), { wrapper: noMotion });
    await act(async () => undefined);
    expect(result.current).toBeUndefined();
  });

  it('in a list: only the first rows, only in the first-appearance window, never nested', async () => {
    let motion = false;
    const seen = new Map<string, unknown>();
    function Row({ id, index }: { id: string; index: number }) {
      seen.set(id, useAppearAnimation(index));
      return null;
    }
    function Nested() {
      seen.set('nested', useAppearAnimation(0));
      return null;
    }
    const now = jest.spyOn(Date, 'now').mockReturnValue(1_000);
    const tree = (rows: { id: string; index: number }[]) => (
      <AppearListScope>
        <MotionWarm onMotion={(v) => (motion = v)} />
        {rows.map((r) => (
          <Row key={r.id} {...r} />
        ))}
        {rows.length > 0 ? (
          <Appear>
            <Nested />
          </Appear>
        ) : null}
      </AppearListScope>
    );
    const first = [
      { id: 'a', index: 0 },
      { id: 'b', index: 5 },
      { id: 'c', index: 6 },
    ];
    const { rerender } = await render(tree([]));
    await waitFor(() => expect(motion).toBe(true));
    await rerender(tree(first));
    expect(seen.get('a')).toBeDefined();
    expect(seen.get('b')).toBeDefined();
    expect(seen.get('c')).toBeUndefined();
    expect(seen.get('nested')).toBeUndefined();

    // A row mounted by scrolling, after the window: no motion.
    now.mockReturnValue(2_000);
    await rerender(tree([...first, { id: 'late', index: 1 }]));
    expect(seen.get('late')).toBeUndefined();
  });

  it('a Card renders its content in both modes', async () => {
    await render(
      <NoMotion>
        <Card testID="card">
          <Text>inside</Text>
        </Card>
      </NoMotion>,
    );
    expect(screen.getByTestId('card')).toBeTruthy();
    expect(screen.getByText('inside')).toBeTruthy();
  });
});

describe('usePressFeedback', () => {
  it('scales on the UI thread when motion is allowed', async () => {
    const { result } = await renderHook(() => usePressFeedback());
    await waitFor(() => expect(result.current.animated).toBe(true));
  });

  it('is an instant opacity change under reduce motion', async () => {
    reduceMotionOn();
    const { result } = await renderHook(() => usePressFeedback());
    await act(async () => undefined);
    expect(result.current.animated).toBe(false);
  });

  it('is an instant opacity change inside NoMotion', async () => {
    const { result } = await renderHook(() => usePressFeedback(), { wrapper: noMotion });
    await act(async () => undefined);
    expect(result.current.animated).toBe(false);
  });
});
