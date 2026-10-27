// Babel for Metro and Jest. NativeWind turns className into styles at build
// time; babel-preset-expo also adds the Reanimated/worklets plugin.
module.exports = function (api) {
  // Jest renders components without the compiled NativeWind stylesheet, so
  // tests skip the className transform; they assert on roles and text, not
  // on styles.
  const isTest = api.env('test');
  api.cache.using(() => isTest);
  return {
    presets: [
      ['babel-preset-expo', isTest ? {} : { jsxImportSource: 'nativewind' }],
      ...(isTest ? [] : ['nativewind/babel']),
    ],
  };
};
