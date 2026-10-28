// Navigation helpers shared by the screens.
import { router } from 'expo-router';

/**
 * goBack returns to the previous screen, or to Home when there is none: a
 * page opened by a direct link on the web has no history to go back to.
 */
export function goBack(): void {
  if (router.canGoBack()) router.back();
  else router.replace('/');
}
