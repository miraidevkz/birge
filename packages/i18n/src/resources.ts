// Every locale file, bundled. Texts ship inside the app so they work offline,
// including the fixed crisis screen (ADR-0023: never depends on the network).
//
// To add a namespace: create <ns>.json in kk/, ru/ and en/, then add it to the
// three objects below. The parity test fails if a language misses a key.
import enAbout from './locales/en/about.json';
import enAdmin from './locales/en/admin.json';
import enAssistant from './locales/en/assistant.json';
import enAuth from './locales/en/auth.json';
import enChat from './locales/en/chat.json';
import enCircle from './locales/en/circle.json';
import enCommon from './locales/en/common.json';
import enDirectory from './locales/en/directory.json';
import enEmergency from './locales/en/emergency.json';
import enHelp from './locales/en/help.json';
import enHome from './locales/en/home.json';
import enInbox from './locales/en/inbox.json';
import enOnboarding from './locales/en/onboarding.json';
import enKnowledge from './locales/en/knowledge.json';
import enPrivacy from './locales/en/privacy.json';
import enReference from './locales/en/reference.json';
import enProfile from './locales/en/profile.json';
import enSharing from './locales/en/sharing.json';
import enSos from './locales/en/sos.json';
import enUi from './locales/en/ui.json';
import enWellbeing from './locales/en/wellbeing.json';
import kkAbout from './locales/kk/about.json';
import kkAdmin from './locales/kk/admin.json';
import kkAssistant from './locales/kk/assistant.json';
import kkAuth from './locales/kk/auth.json';
import kkChat from './locales/kk/chat.json';
import kkCircle from './locales/kk/circle.json';
import kkCommon from './locales/kk/common.json';
import kkDirectory from './locales/kk/directory.json';
import kkEmergency from './locales/kk/emergency.json';
import kkHelp from './locales/kk/help.json';
import kkHome from './locales/kk/home.json';
import kkInbox from './locales/kk/inbox.json';
import kkOnboarding from './locales/kk/onboarding.json';
import kkKnowledge from './locales/kk/knowledge.json';
import kkPrivacy from './locales/kk/privacy.json';
import kkReference from './locales/kk/reference.json';
import kkProfile from './locales/kk/profile.json';
import kkSharing from './locales/kk/sharing.json';
import kkSos from './locales/kk/sos.json';
import kkUi from './locales/kk/ui.json';
import kkWellbeing from './locales/kk/wellbeing.json';
import ruAbout from './locales/ru/about.json';
import ruAdmin from './locales/ru/admin.json';
import ruAssistant from './locales/ru/assistant.json';
import ruAuth from './locales/ru/auth.json';
import ruChat from './locales/ru/chat.json';
import ruCircle from './locales/ru/circle.json';
import ruCommon from './locales/ru/common.json';
import ruDirectory from './locales/ru/directory.json';
import ruEmergency from './locales/ru/emergency.json';
import ruHelp from './locales/ru/help.json';
import ruHome from './locales/ru/home.json';
import ruInbox from './locales/ru/inbox.json';
import ruOnboarding from './locales/ru/onboarding.json';
import ruKnowledge from './locales/ru/knowledge.json';
import ruPrivacy from './locales/ru/privacy.json';
import ruReference from './locales/ru/reference.json';
import ruProfile from './locales/ru/profile.json';
import ruSharing from './locales/ru/sharing.json';
import ruSos from './locales/ru/sos.json';
import ruUi from './locales/ru/ui.json';
import ruWellbeing from './locales/ru/wellbeing.json';

/** Namespaces, one per feature. `common` is the default. */
export const namespaces = [
  'common',
  'ui',
  'auth',
  'admin',
  'home',
  'inbox',
  'about',
  'reference',
  'onboarding',
  'help',
  'circle',
  'chat',
  'sharing',
  'directory',
  'profile',
  'privacy',
  'knowledge',
  'assistant',
  'wellbeing',
  'emergency',
  'sos',
] as const;

/** Namespace is one locale file name. */
export type Namespace = (typeof namespaces)[number];

/** resources maps language to namespace to the parsed locale file. */
export const resources = {
  kk: {
    common: kkCommon,
    ui: kkUi,
    auth: kkAuth,
    admin: kkAdmin,
    home: kkHome,
    inbox: kkInbox,
    about: kkAbout,
    reference: kkReference,
    onboarding: kkOnboarding,
    help: kkHelp,
    circle: kkCircle,
    chat: kkChat,
    sharing: kkSharing,
    directory: kkDirectory,
    profile: kkProfile,
    privacy: kkPrivacy,
    knowledge: kkKnowledge,
    assistant: kkAssistant,
    wellbeing: kkWellbeing,
    emergency: kkEmergency,
    sos: kkSos,
  },
  ru: {
    common: ruCommon,
    ui: ruUi,
    auth: ruAuth,
    admin: ruAdmin,
    home: ruHome,
    inbox: ruInbox,
    about: ruAbout,
    reference: ruReference,
    onboarding: ruOnboarding,
    help: ruHelp,
    circle: ruCircle,
    chat: ruChat,
    sharing: ruSharing,
    directory: ruDirectory,
    profile: ruProfile,
    privacy: ruPrivacy,
    knowledge: ruKnowledge,
    assistant: ruAssistant,
    wellbeing: ruWellbeing,
    emergency: ruEmergency,
    sos: ruSos,
  },
  en: {
    common: enCommon,
    ui: enUi,
    auth: enAuth,
    admin: enAdmin,
    home: enHome,
    inbox: enInbox,
    about: enAbout,
    reference: enReference,
    onboarding: enOnboarding,
    help: enHelp,
    circle: enCircle,
    chat: enChat,
    sharing: enSharing,
    directory: enDirectory,
    profile: enProfile,
    privacy: enPrivacy,
    knowledge: enKnowledge,
    assistant: enAssistant,
    wellbeing: enWellbeing,
    emergency: enEmergency,
    sos: enSos,
  },
} as const satisfies Record<string, Record<Namespace, object>>;
