# Glossary

The words this project uses, in plain terms. Where the app shows a Russian name, it is given in «quotes». Product words first, then the technical ones.

## Product

**Member.** A mother or other caregiver who uses the mom app. A new member can use most of the app; a moderator **verifies** her before she can take part in Tea, chats, listings and reviews. A moderator can **suspend** a member who breaks the rules.

**Mentor.** An experienced, verified member whom staff give the mentor role. In the group rooms staff assign her to, she can hide and restore messages and mute someone for an hour.

**Check-in** («Как я»). The weekly two-minute well-being questionnaire (PHQ-4 and PSS-4). A high score sets the `elevated` flag, and the app offers to contact a psychologist.

**Talk** («Поговорить») and **the assistant.** A supportive conversation with the AI assistant. It answers only from the reviewed knowledge base and never diagnoses.

**Knowledge base.** Articles reviewed by experts: **rights** («Права», written and published by lawyers, reviewed every 6 months) and **self-help** (by psychologists, every 12 months). An article that is due for review shows up in the staff admin.

**Moms nearby** («Мамы рядом») and **Tea** («Чай»). A member asks for Tea: to meet or call another mother from her district. Every hour open requests are paired; when both accept, they get a private chat. In the code this is the `circle` context.

**Met in person** («знакомы лично»). A status of a pair of mothers, never of one mother: both confirmed after Tea that they met. Only a member she has met in person can be her trusted contact, and only such a member may ever be suggested for childcare (non-negotiable 5).

**Trusted contact.** Someone in her emergency plan who gets her SOS: a member she has met in person, or an outside person by phone number, who gets an SMS without content and a one-time link.

**Emergency plan** («План ЧС»). Her plan for a bad day: the child card and her trusted contacts. She can send it to an outside contact as a one-time link.

**SOS.** An urgent request for practical help: no one to leave the child with, an item or medicine, other. It goes at once to the moderators and her trusted contacts. A verified member may also share it to her district feed, except a childcare SOS, which is never shared.

**Escalation.** Handing a member to a human. "I feel very bad", a crisis found in the assistant, chat or check-in, and "contact a person" all open an escalation for the on-duty psychologist. If nobody takes it within 15 minutes, the backup psychologist is alerted.

**Duty roster.** The shifts of psychologists and moderators. The on-duty person is whoever's shift covers now.

**Crisis lexicon.** The list of words and phrases, per language, that signal a crisis. It is checked on our server before any AI call (level 1, "L1"); the model's own crisis flag is level 2. A match shows the **fixed crisis response**: a pre-written text with helpline numbers that never depends on the AI.

**Moderation queue.** Reported and automatically held content waiting for a moderator's decision.

**Legal hold.** A mark on content that may be evidence of a crime: it is kept and hidden instead of being deleted on schedule.

## Privacy and data

**Personal data.** Anything that identifies a member: name, phone, district, children, messages. It stays in the country of the deployment.

**Anonymization.** Replacing names, phone numbers, ID numbers and addresses in a message with placeholders before it reaches the AI model.

**Cross-border consent.** A separate consent a member gives before any anonymized text of hers is sent to an AI model outside the country.

**Per-member key and crypto-shredding.** Sensitive fields are encrypted with a key of their own for each member. Deleting the account destroys the key, so even old backups become unreadable for her.

**Tombstone.** What remains of a deleted record when other records still point to it: the row and its ID stay, the content is cleared. The other mother's chat history keeps a "message deleted" line instead of a gap.

**Retention.** How long a kind of data is kept before its content is cleared automatically, set per context in an ADR.

**Demo preset.** Example data built into the binary so a fresh install works (crisis lexicon, questionnaire, texts, reference data). A real deployment must replace every preset.

## Technical

**Bounded context.** One area of the product with its own code (`internal/domains/<context>`), its own tables and its own README. Contexts never read each other's tables.

**Contract.** The description of the HTTP API (`contracts/openapi/birge.yaml`) and of events (`contracts/events/`). It changes before the code, and server and client code are generated from it.

**Domain event and outbox.** A fact one context tells the others, such as `circle.MatchAccepted`. It is written in the same database transaction as the change and then delivered through NATS, so it is never lost.

**Timer.** Work scheduled for later (a reminder, the 15-minute escalation backup), stored in PostgreSQL so it survives restarts.

**Non-negotiables.** The six rules in [AGENTS.md](../AGENTS.md) that no change may break; [overview.md](overview.md#non-negotiables-plain-words) explains them in plain words.
