// Package crisis is the level-1 crisis detector of the AI pipeline
// (docs/requirements/mvp.ru.md § 5 step 1; ADR-0023, ADR-0017): a local,
// deterministic lexicon match that runs before anonymization and before any
// network call, so that the crisis response never depends on the LLM.
//
// # Normalisation
//
// Both the message and every lexicon phrase go through the same normaliser
// (Normalize), so lexicon authors write natural text and the matcher sees one
// reduced alphabet:
//
//   - Latin and digit look-alikes inside Cyrillic words become Cyrillic
//     letters (a→а, e→е, o→о, p→р, c→с, x→х, y→у, k→к, 0→о, 3→з, 4→ч, 6→б, …).
//     A word without Cyrillic letters is mapped only when it consists entirely
//     of look-alikes and the message contains Cyrillic elsewhere; ordinary
//     Latin words are left alone;
//   - simple Unicode case folding (unicode.ToLower) and removal of combining
//     marks;
//   - Kazakh letters fold to their Russian keyboard look-alikes, because many
//     members type Kazakh on a Russian layout: ә→а, ғ→г, қ→к, ң→н, ө→о,
//     ұ→у, ү→у, һ→х, і→и;
//   - common Russian spelling variants fold together: ё→е, й→и, э→е, щ→ш,
//     and ь, ъ are dropped (the most frequent typo is a missing soft sign);
//   - every character that is not a letter separates words;
//   - runs of the same letter collapse to one (умерееееть → умерет).
//
// # Matching
//
// A lexicon is a set of phrases and regular expressions per language and
// category (see Load for the file format). A message matches a category when
// any of these holds:
//
//   - a phrase matches a contiguous run of words. A word matches exactly,
//     or by prefix when the phrase word ends in '*', or with one edit
//     (insertion, deletion, substitution or transposition) when the phrase
//     word has at least FuzzyMinLen letters;
//   - the phrase, with all spaces removed, is a substring of the message with
//     all spaces removed, when that compact phrase has at least
//     CompactMinLen letters. This catches s p a c e d and punctuated
//     (у.м.е.р.е.т.ь) spellings;
//   - a regular expression matches the normalised message: words separated by
//     single spaces, with one leading and one trailing space, so a pattern
//     marks word boundaries with \s (RE2's \b is ASCII-only).
//
// Every loaded language is checked, the caller's language first. Mixed
// Kazakh-Russian messages are common and a missed crisis costs far more than
// a false alert (the false-positive rate is tracked by the evaluation set).
//
// # Invariants
//
//   - Detect is pure: no I/O, no logging, no shared mutable state; a Lexicon
//     is immutable after Load and safe for concurrent use.
//   - The message text is never logged, stored or returned by this package.
//   - The production lexicon is data supplied by the project psychologist and
//     kept outside the repository; testdata/ holds a small DEMO lexicon only.
package crisis
