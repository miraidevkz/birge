// Route names the root stack treats specially. Kept apart from the layout so
// tests can check them without rendering the whole app.

/**
 * ROOT_ROUTES are roots of the stack: everything else is stacked above them
 * and gets Back. `plan` is a root because it is a standalone page for an
 * external trusted contact (no account, opened from an SMS): Back would lead
 * into the app, where the contact has no account.
 */
export const ROOT_ROUTES: ReadonlySet<string> = new Set(['(tabs)', 'sign-in', 'onboarding', 'setup', 'plan']);
