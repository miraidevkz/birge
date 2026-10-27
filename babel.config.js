// Jest runs from the repository root and loads the Babel config found here
// (jest-expo resolves it from the working directory). Metro loads the app's own
// config. Both must transform code the same way, so this file only re-exports it.
module.exports = require('./apps/mobile/babel.config.js');
