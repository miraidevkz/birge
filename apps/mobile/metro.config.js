// Metro: Expo defaults (which already watch the npm workspace packages) plus
// NativeWind, which compiles global.css and the token preset.
const { getDefaultConfig } = require('expo/metro-config');
const { withNativeWind } = require('nativewind/metro');

const config = getDefaultConfig(__dirname);

module.exports = withNativeWind(config, { input: './global.css' });
