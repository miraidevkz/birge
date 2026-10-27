// Tailwind preset that exposes tokens.js as NativeWind classes. Apps add it to
// their tailwind.config.js `presets` after `nativewind/preset`.
//
// Colors are CSS variables holding "r g b", set on :root for the light scheme
// and under prefers-color-scheme: dark for the dark one. Components then use
// one class (`bg-background`, `text-foreground`) for both schemes, and opacity
// modifiers (`bg-primary/50`) keep working.
//
// The theme replaces Tailwind's default scales instead of extending them, so
// a class outside the design system (`p-7`, `text-xl`, `bg-red-500`) simply
// does not exist.
const tokens = require('./tokens');

function hexToChannels(hex) {
  const n = parseInt(hex.slice(1), 16);
  return `${(n >> 16) & 255} ${(n >> 8) & 255} ${n & 255}`;
}

function cssVar(name) {
  return '--color-' + name.replace(/[A-Z]/g, (c) => '-' + c.toLowerCase());
}

function variables(scheme) {
  return Object.fromEntries(Object.entries(scheme).map(([k, v]) => [cssVar(k), hexToChannels(v)]));
}

const px = (scale) => Object.fromEntries(Object.entries(scale).map(([k, v]) => [k, `${v}px`]));

const colorClasses = Object.fromEntries(
  Object.keys(tokens.colors.light).map((k) => [
    k.replace(/[A-Z]/g, (c) => '-' + c.toLowerCase()),
    `rgb(var(${cssVar(k)}) / <alpha-value>)`,
  ]),
);

/** @type {import('tailwindcss').Config} */
module.exports = {
  darkMode: 'media',
  theme: {
    colors: { transparent: 'transparent', ...colorClasses },
    spacing: px(tokens.spacing),
    borderRadius: { none: '0px', ...px(tokens.radius) },
    fontFamily: {
      sans: [tokens.fontFamily.regular],
      'sans-semibold': [tokens.fontFamily.semibold],
      'sans-bold': [tokens.fontFamily.bold],
    },
    fontSize: Object.fromEntries(
      Object.entries(tokens.fontSize).map(([k, [size, line]]) => [k, [`${size}px`, { lineHeight: `${line}px` }]]),
    ),
    transitionDuration: Object.fromEntries(Object.entries(tokens.motion).map(([k, v]) => [k, `${v}ms`])),
    extend: {
      minHeight: { touch: `${tokens.touchTarget}px` },
      minWidth: { touch: `${tokens.touchTarget}px` },
    },
  },
  plugins: [
    function birgeColorVariables({ addBase }) {
      addBase({
        ':root': variables(tokens.colors.light),
        '@media (prefers-color-scheme: dark)': { ':root': variables(tokens.colors.dark) },
      });
    },
  ],
};
