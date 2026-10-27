// Classes come only from the Birge token preset (packages/ui/tailwind-preset.js).
/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ['./src/**/*.{ts,tsx}', '../../packages/ui/src/**/*.{ts,tsx}'],
  presets: [require('nativewind/preset'), require('@birge/ui/tailwind-preset')],
};
