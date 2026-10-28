// Flat vector scenes for onboarding and success moments (ADR-0025). Every
// scene is built from the brand mark (branding/render.py geometry: six dots of
// radius 40 on a ring of radius 124, 512 grid) and soft rounded shapes, in
// theme colours only, so it follows dark mode with no image files.
//
// Discretion (branding/README.md): no faces, nothing medical or diagnostic and
// nothing that tells an onlooker what the app is about. Decorative: hidden from
// screen readers, the text next to it carries the meaning. Static on purpose,
// so it is safe on crisis screens (no animation there, ADR-0014).
import { View } from 'react-native';
import Svg, { Circle, G, Path, Rect } from 'react-native-svg';

import { tokens, useThemeColors, type ThemeColors } from './theme';

/** IllustrationName is one of the scenes. */
export type IllustrationName = 'welcome' | 'inside' | 'privacy' | 'done';

/** IllustrationProps: which scene and how large (a token from tokens.illustration). */
export interface IllustrationProps {
  name: IllustrationName;
  size?: keyof typeof tokens.illustration;
  testID?: string;
}

// The mark's geometry on the 512 grid (branding/render.py).
const RING = 124;
const DOT = 40;
const RING_STROKE = 16;

/**
 * Mark draws the six dots on their ring centred at (cx, cy), scaled; the
 * same shape as the Logo and the app icon at every size.
 */
function Mark({
  cx,
  cy,
  scale,
  fill,
  ring,
  ringOpacity = 0.45,
}: {
  cx: number;
  cy: number;
  scale: number;
  fill: string;
  ring: string;
  ringOpacity?: number;
}) {
  return (
    <G>
      <Circle
        cx={cx}
        cy={cy}
        r={RING * scale}
        fill="none"
        stroke={ring}
        strokeOpacity={ringOpacity}
        strokeWidth={RING_STROKE * scale}
      />
      {[0, 1, 2, 3, 4, 5].map((k) => {
        const a = ((-90 + 60 * k) * Math.PI) / 180;
        return (
          <Circle
            key={k}
            cx={cx + RING * scale * Math.cos(a)}
            cy={cy + RING * scale * Math.sin(a)}
            r={DOT * scale}
            fill={fill}
          />
        );
      })}
    </G>
  );
}

/** Welcome: the circle of six around a warm round table, on a sand ground. */
function Welcome({ c }: { c: ThemeColors }) {
  const dots = [0, 1, 2, 3, 4, 5].map((k) => (-90 + 60 * k) * (Math.PI / 180));
  return (
    <G>
      <Circle cx={256} cy={268} r={208} fill={c.accent} />
      <Circle cx={256} cy={268} r={150} fill="none" stroke={c.primary} strokeOpacity={0.3} strokeWidth={10} />
      <Circle cx={256} cy={268} r={88} fill={c.card} />
      <Mark cx={256} cy={268} scale={0.36} fill={c.primary} ring={c.primary} />
      {dots.map((a, k) => (
        <Circle
          key={k}
          cx={256 + 150 * Math.cos(a)}
          cy={268 + 150 * Math.sin(a)}
          r={34}
          fill={c.primary}
          fillOpacity={k % 2 === 0 ? 1 : 0.6}
        />
      ))}
      <Circle cx={430} cy={92} r={26} fill={c.accentForeground} fillOpacity={0.25} />
      <Circle cx={84} cy={440} r={16} fill={c.accentForeground} fillOpacity={0.2} />
    </G>
  );
}

/** Inside: three soft cards, like the three things the app offers, without naming them. */
function Inside({ c }: { c: ThemeColors }) {
  const rows = [
    { y: 112, dot: 1 },
    { y: 218, dot: 0.6 },
    { y: 324, dot: 0.35 },
  ];
  return (
    <G>
      <Circle cx={256} cy={256} r={212} fill={c.accent} />
      {rows.map(({ y, dot }) => (
        <G key={y}>
          <Rect x={96} y={y} width={320} height={84} rx={28} fill={c.card} />
          <Circle cx={142} cy={y + 42} r={22} fill={c.primary} fillOpacity={dot} />
          <Rect x={182} y={y + 24} width={180} height={14} rx={7} fill={c.muted} />
          <Rect x={182} y={y + 48} width={120} height={12} rx={6} fill={c.separator} />
        </G>
      ))}
    </G>
  );
}

/** Privacy: a rounded lock whose keyhole is the mark. */
function Privacy({ c }: { c: ThemeColors }) {
  return (
    <G>
      <Circle cx={256} cy={256} r={212} fill={c.accent} />
      <Path
        d="M188 236 V188 a68 68 0 0 1 136 0 V236"
        fill="none"
        stroke={c.primary}
        strokeWidth={30}
        strokeLinecap="round"
      />
      <Rect x={150} y={226} width={212} height={176} rx={44} fill={c.primary} />
      <Mark cx={256} cy={314} scale={0.3} fill={c.primaryForeground} ring={c.primaryForeground} ringOpacity={0.5} />
    </G>
  );
}

/** Done: the mark with a soft check inside, on a sand ground. */
function Done({ c }: { c: ThemeColors }) {
  return (
    <G>
      <Circle cx={256} cy={256} r={212} fill={c.accent} />
      <Mark cx={256} cy={256} scale={1} fill={c.primary} ring={c.primary} />
      <Path
        d="M206 258 L240 292 L308 222"
        fill="none"
        stroke={c.primary}
        strokeWidth={24}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </G>
  );
}

const scenes = { welcome: Welcome, inside: Inside, privacy: Privacy, done: Done } as const;

/**
 * Illustration is a decorative, static scene in the brand style (ADR-0025):
 * `welcome`, `inside` and `privacy` for the onboarding steps, `done` beside a
 * success message. Colours come from the current theme; it is hidden from
 * assistive technologies, so the surrounding text must say what happened.
 */
export function Illustration({ name, size = 'md', testID }: IllustrationProps) {
  const colors = useThemeColors();
  const px = tokens.illustration[size];
  const Scene = scenes[name];
  return (
    <View
      className="self-center"
      // Square box from the size token; the scene scales into it.
      style={{ width: px, height: px }}
      aria-hidden
      accessibilityElementsHidden
      importantForAccessibility="no-hide-descendants"
      testID={testID ?? `illustration-${name}`}
    >
      <Svg width="100%" height="100%" viewBox="0 0 512 512">
        <Scene c={colors} />
      </Svg>
    </View>
  );
}
