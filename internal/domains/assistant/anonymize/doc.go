// Package anonymize replaces personal data in a member's message with labels
// before the text leaves the jurisdiction (docs/requirements/mvp.md N2, § 5
// step 2; ADR-0013, ADR-0017). It is pure: no I/O, no logging, no shared
// mutable state; an Anonymizer is immutable after New and safe for
// concurrent use.
//
// # What is replaced
//
// In this order, so that a later rule never sees what an earlier one removed:
//
//  1. e-mail addresses → [EMAIL] (also with spaces around '@');
//  2. street addresses → [ADDRESS]: a street keyword in Russian, Kazakh or
//     English (ул., улица, проспект, мкр., көшесі, даңғылы, street, avenue,
//     …) next to a house number, optionally with building and flat
//     ("д. 5, кв. 12"), and "дом/үй N кв./пәтер M" on its own;
//  3. letter-prefixed document numbers (N12345678, АА1234567) → [DOC];
//  4. runs of digits joined by spaces, dashes, dots or brackets, classified
//     by their digit count: 12 → [IIN]; 11 starting with 7 or 8, 10
//     starting with 7, anything starting with '+', or a local NN-NN-NN form
//     → [PHONE]; 7 or more otherwise → [DOC]. Dates (12.05.2024), year
//     ranges and amounts grouped by thousands (150 000) are kept, because
//     legal questions need them;
//  5. names → [NAME]: the member's own display name and a dictionary of
//     common names supplied as data (Settings in the service; a test list in
//     testdata). Each name is matched in the case forms we can produce
//     without a morphological dictionary: Russian declension endings on the
//     name or its stem without the final vowel, й or ь (Анна → Анны, Анне,
//     Анну, Анной; Сергей → Сергея; Арман → Армана, Арманом), and Kazakh
//     suffixes appended to the full name, alone or after a possessive
//     (Арманның, Арманға, Арманды, Арманда, Арманнан, Арманмен,
//     Арманымның). Matching is case-insensitive and folds ё→е and Kazakh
//     letters to their Russian-keyboard look-alikes (ә→а, ғ→г, қ→к, ң→н,
//     ө→о, ұ/ү→у, һ→х, і→и), as members often type Kazakh on a Russian
//     layout.
//
// # Limits (known gaps, tracked by evals/datasets/anonymization)
//
// Diminutives (Анечка), nicknames and names missing from the dictionary are
// not recognised; an address without a keyword or a number is kept; spelled-
// out digits are kept. Over-masking is preferred to a leak: a common word
// that is also a dictionary name (Роза, Алма) becomes [NAME].
package anonymize
