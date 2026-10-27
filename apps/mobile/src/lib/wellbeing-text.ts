// Questionnaire and feedback keys come from the server as opaque locale keys
// (contract: texts live in client locale files). Keys may be written with or
// without the "wellbeing." namespace prefix (demo preset vs unit fixtures).

const NS_PREFIX = 'wellbeing.';

/** wellbeingKey turns a server textKey/feedbackKey into a key inside the wellbeing namespace. */
export function wellbeingKey(raw: string): string {
  return raw.startsWith(NS_PREFIX) ? raw.slice(NS_PREFIX.length) : raw;
}
