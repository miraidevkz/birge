// Root of the app: providers, fonts and the root stack. Only route concerns
// live in src/app; screen bodies are in src/screens.
import '../../global.css';

import { initI18n } from '@birge/i18n';
import { fonts, ScreenBackProvider, SosProvider } from '@birge/ui';
import { QueryClientProvider } from '@tanstack/react-query';
import { useFonts } from 'expo-font';
import { getLocales } from 'expo-localization';
import { router, Stack } from 'expo-router';
import { StatusBar } from 'expo-status-bar';
import { useEffect, useState } from 'react';
import { KeyboardProvider } from 'react-native-keyboard-controller';
import { SafeAreaProvider } from 'react-native-safe-area-context';

import { kratos } from '@/lib/api';
import { loadCrisisPhones, refreshCrisisPhones } from '@/lib/crisis-phones';
import { flushOutbox, startOutboxReplay } from '@/lib/help-outbox';
import { loadSavedLanguage } from '@/lib/language';
import { goBack } from '@/lib/nav';
import { createQueryClient, syncFocusWithAppState } from '@/lib/query';
import { SessionProvider, useSession } from '@/lib/session';

// Synchronous: texts are bundled, so the first frame is already translated.
// Routes that are roots: everything else is stacked above them and gets Back.
const ROOTS = new Set(['(tabs)', 'sign-in', 'onboarding']);

initI18n(getLocales().map((l) => l.languageTag));

const openHelp = () => router.push('/sos');

// A request queued while the session had expired goes out right after the
// member signs in again.
function FlushOnSignIn() {
  const { signedIn } = useSession();
  useEffect(() => {
    if (signedIn) void flushOutbox();
  }, [signedIn]);
  return null;
}

export default function RootLayout() {
  const [queryClient] = useState(createQueryClient);
  const [fontsLoaded, fontError] = useFonts(fonts);

  useEffect(() => {
    // Crisis numbers first: stored list into memory, then a fresh one if online.
    void loadSavedLanguage();
    void loadCrisisPhones().then(refreshCrisisPhones);
    const stopReplay = startOutboxReplay();
    const stopFocus = syncFocusWithAppState();
    return () => {
      stopReplay();
      stopFocus();
    };
  }, []);

  // Fonts are bundled and load in milliseconds; on failure the system font
  // is used rather than blocking the app.
  if (!fontsLoaded && !fontError) return null;

  return (
    <SafeAreaProvider>
      <KeyboardProvider>
        <QueryClientProvider client={queryClient}>
          <SessionProvider revoke={kratos.signOut}>
            <SosProvider onPress={openHelp}>
              <StatusBar style="auto" />
              <FlushOnSignIn />
              <Stack
                screenOptions={{ headerShown: false }}
                screenLayout={({ children, route }) =>
                  ROOTS.has(route.name) ? children : <ScreenBackProvider onBack={goBack}>{children}</ScreenBackProvider>
                }
              >
                <Stack.Screen name="(tabs)" />
                <Stack.Screen name="onboarding" />
                <Stack.Screen name="sign-in" />
                <Stack.Screen name="privacy" />
                <Stack.Screen name="about-me" />
                <Stack.Screen name="knowledge" />
                <Stack.Screen name="knowledge-article/[articleId]" />
                <Stack.Screen name="assistant" />
                <Stack.Screen name="checkin" />
                <Stack.Screen name="checkin-history" />
                <Stack.Screen name="emergency-plan" />
                {/* Crisis / SOS: no transition animation (ADR-0014). */}
                <Stack.Screen name="sos" options={{ animation: 'none', presentation: 'fullScreenModal' }} />
              </Stack>
            </SosProvider>
          </SessionProvider>
        </QueryClientProvider>
      </KeyboardProvider>
    </SafeAreaProvider>
  );
}
