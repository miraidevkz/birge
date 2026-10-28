# Content style: how Birge speaks

Birge is used by mothers of children with special needs, many of them exhausted or in distress. Every string in the apps is read by someone who may be having the hardest day of her week. This guide sets the tone for all UI text in `packages/i18n/src/locales/{kk,ru,en}/`. It is the copy side of [ADR-0025](adr/0025-warm-design-language.md) (warm design language); the review list of the first pass is [content-review-2026-10.md](content-review-2026-10.md).

Who checks it: the author of a string follows this guide; the project psychologist and a native speaker of each language review new and changed texts before release.

## Principles

### 1. Warm and plain
Speak like a calm, kind person, not like a system. Everyday words, no jargon, no officialese.

| Instead of | Write |
| --- | --- |
| ru: «Дневной лимит сообщений исчерпан.» | «На сегодня сообщения закончились. Завтра можно продолжить.» |
| en: "Daily message limit reached." | "That is all the messages for today. You can continue tomorrow." |
| kk: «Күнделікті хабарлама шегіне жеттіңіз.» | «Бүгінгі хабарламалар таусылды. Ертең жалғастыруға болады.» |

### 2. Invitations, not orders
Offer, do not command. Prefer «можно», «попробуйте», «сұрауға болады», "you can". A plain imperative is fine for a button or a field label ("Save", «Сохранить»); in sentences, invite.

- ru: «Можно попросить удалить все ваши данные.» rather than «Запросите удаление всех ваших данных.»
- kk: «Барлық деректеріңізді жоюды сұрауға болады.»
- en: "You can ask us to delete all your data."

### 3. No bans, no blame
Avoid «нельзя», «запрещено», «неверно», «ошибка», «не разрешено», «тиым салынған», «қате», "not allowed", "invalid", "wrong". Describe what happened to the thing, not what the member did wrong.

| Instead of | Write |
| --- | --- |
| ru: «Неверный код.» | «Код не подошёл.» |
| kk: «Код қате.» | «Код сәйкес келмеді.» |
| en: "The code is not correct." | "That code did not match." |
| ru: «Введите правильный номер.» | «Проверьте номер: он нужен в международном формате…» |

When a limit is real (an irreversible deletion, the three-contact maximum, the met-in-person rule), say it honestly as a consequence or a condition, never as a prohibition: «После удаления вернуть данные не получится.»; "A member can become a contact if you have met in person."

### 4. Empty states say what will appear
An empty list is a promise, not a void. Say what will appear here and, where useful, the next step. Never a bare «Нет …», «… жоқ», "No …"; «Пока нет …» / «Әзірге … жоқ» / "No … yet" is acceptable when the description carries the promise.

- ru: «Здесь пока тихо» — «Здесь появятся сообщения и напоминания.» (not «Ничего нового»)
- kk: «Әзірге тыныш» — «Хабарламалар мен еске салғыштар осында көрінеді.»
- en: "All quiet for now" — "Messages and reminders will appear here."

### 5. Errors are honest, never her fault, always with a next step
Say plainly that something did not work, do not hide it, and always give one thing she can do: try again, check the connection, where to go instead.

- ru: «Не удалось загрузить план. Попробуйте ещё раз чуть позже.»
- kk: «Жоспарды жүктеу мүмкін болмады. Сәлден кейін қайталап көріңіз.»
- en: "Could not load the plan. Please try again in a moment."

A rate limit is a pause, not a reproach: «Давайте сделаем паузу. Через минуту можно попробовать снова.» / «Сәл үзіліс жасайық.» / "Let us take a short pause."

### 6. No labels about the child or the mother
Never «больной», «проблемный», «диагноз», «отказ», «нарушения», «ауру», «кемістік», "sick", "problem child", "disorder" about a child or a mother. Use «особенности» / «ерекшеліктері» / "needs". A legal text may use the term the law uses (for example «инвалидность» / «мүгедектік» / "disability" in a benefit's name); that is the knowledge base's call, not UI copy.

The wellbeing check-in may say «Это не диагноз.» / «Бұл диагноз емес.» / "This is not a diagnosis.": a reassurance, not a label.

### 7. Short sentences
One idea per sentence; two short sentences beat one long one. Keep translations roughly the same length so layouts hold in all three languages.

### 8. Formal "you", always
ru: «Вы» in the formal form (written in lower case in running text: «вы», «ваш»), never «ты». kk: «Сіз» and its forms (-ңыз/-ңіз, -ыңыз/-іңіз), never «сен». en has no distinction; keep the same respectful register.

### 9. Parallel languages
kk, ru and en carry the same meaning and the same placeholders (`{{name}}`) under the same key. Write each one naturally rather than word for word. kk is reviewed by a native speaker before release.

## Exclusions: never edited for tone

These texts follow other rules. If one looks harmful, record it in the current content review file for the psychologist; never rewrite it in a copy pass.

1. **The wellbeing questionnaire** — `wellbeing.phq4.*`, `wellbeing.pss4.*` (items and answer options), the scoring texts referenced by the questionnaire preset (`wellbeing.feedback.*`, see `presets/demo/questionnaire.json`) and `wellbeing.history.point`. PHQ-4 and PSS-4 are validated instruments: changing their wording breaks their validity. Only a validated translation, chosen by the psychologist, may replace them.
2. **Crisis texts** — the `help` and `sos` namespaces, the crisis banners in chat and the assistant (`chat.room.held`, `chat.room.crisisAction`, `assistant.crisis.*`, `assistant.degraded.*`) and the escalation to the psychologist after a check-in (`wellbeing.elevated.*`, which mirrors `help.status`). They must stay direct and unmistakable ("call 112"); softening them can cost time in an emergency. Changes go through the crisis-path review (`.claude/skills/crisis-path`), the psychologist and native speakers.
3. **Legal texts** — articles in the knowledge base under "My rights" (`knowledge/rights`). They quote the law and its terms; accuracy wins over tone. Their editors and reviewers own them.
4. **Required disclosures** — `assistant.intro` ("I am a bot and an AI assistant, not a doctor…") must stay explicit.

## Checklist for a new string

- Would a tired, worried mother feel addressed kindly?
- No ban, blame or label words; no bare "No …".
- An error or an empty state tells her what comes next.
- Short; formal «Вы»/«Сіз»; same meaning and placeholders in kk, ru and en.
- Not one of the exclusions above.
