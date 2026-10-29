// Root of the staff admin: providers, fonts and the root stack. Only route
// concerns live in src/app; screen bodies are in src/screens. No SosProvider:
// this is not the member crisis path.
import '../../global.css';

import { initI18n } from '@birge/i18n';
import { fonts, ScreenWidthProvider } from '@birge/ui';
import { QueryClientProvider } from '@tanstack/react-query';
import { useFonts } from 'expo-font';
import { getLocales } from 'expo-localization';
import { Stack } from 'expo-router';
import { StatusBar } from 'expo-status-bar';
import { useEffect, useState } from 'react';
import { KeyboardProvider } from 'react-native-keyboard-controller';
import { SafeAreaProvider } from 'react-native-safe-area-context';

import { createQueryClient, syncFocusWithAppState } from '@/lib/query';
import { SessionProvider } from '@/lib/session';

initI18n(getLocales().map((l) => l.languageTag));

export default function RootLayout() {
  const [queryClient] = useState(createQueryClient);
  const [fontsLoaded, fontError] = useFonts(fonts);

  useEffect(() => {
    const stopFocus = syncFocusWithAppState();
    return () => stopFocus();
  }, []);

  if (!fontsLoaded && !fontError) return null;

  return (
    <SafeAreaProvider>
      <KeyboardProvider>
        <QueryClientProvider client={queryClient}>
          <SessionProvider>
            {/* A work console on desktops: queues and filters get the wide column. */}
            <ScreenWidthProvider width="wide">
              <StatusBar style="auto" />
              <Stack screenOptions={{ headerShown: false }}>
                <Stack.Screen name="(tabs)" />
                <Stack.Screen name="sign-in" />
              </Stack>
            </ScreenWidthProvider>
          </SessionProvider>
        </QueryClientProvider>
      </KeyboardProvider>
    </SafeAreaProvider>
  );
}
