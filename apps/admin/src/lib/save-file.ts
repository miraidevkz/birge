// Saving a file the console downloaded (the metrics CSV). The console is web
// only (README), so this hands the browser a Blob through an object URL and a
// temporary link with `download`; the data never touches storage, logs or the
// query cache. The URL is revoked shortly after the click, once the browser
// has started the save.
import { Platform } from 'react-native';

// Revoking at once can cancel the save in some browsers; a moment later is safe.
const REVOKE_AFTER_MS = 1000;

/**
 * saveTextFile offers text to the user as a file named filename with the
 * given media type. It throws outside a browser, where there is no download.
 */
export function saveTextFile(text: string, filename: string, type: string): void {
  if (Platform.OS !== 'web' || typeof document === 'undefined') {
    throw new Error('saving a file needs a browser');
  }
  const url = URL.createObjectURL(new Blob([text], { type }));
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  link.rel = 'noopener';
  document.body.appendChild(link);
  try {
    link.click();
  } finally {
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), REVOKE_AFTER_MS);
  }
}
