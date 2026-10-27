# ADR-0004: React Native for all clients

Status: Accepted · Date: 2026-10-27

## Context
The mom app must be published to App Store and Google Play; a PWA will not be installed by users and has weak push on iOS. A Telegram bot stores conversations outside the jurisdiction. The staff admin should not be written twice.

## Decision
Expo / React Native monorepo: `apps/mobile` (iOS, Android, web) and `apps/admin` (web via react-native-web), sharing `packages/*`. Admin and mom app are separate bundles. Push goes directly through FCM/APNs. Builds run in our CI (`expo prebuild` + Gradle/Xcode); no EAS or other Expo cloud services, no Expo account required; Expo CLI telemetry is disabled (`EXPO_NO_TELEMETRY=1`). OTA updates, if used, come from a self-hosted server implementing the open expo-updates protocol.

## Consequences
The Expo framework (SDK, Router, CLI) is MIT and runs locally; only its cloud services are excluded. Keeping React Native depends on meeting the performance budget on a low-end Android device (cold start, scrolling, memory); this is measured in wave 1.
Data grids in the admin are weaker than in plain React web; acceptable. Native app enables a full decoy mode (optional feature).
