# Content review, October 2026

First tone pass over all UI strings against [content-style.md](content-style.md). For the project psychologist and a native speaker of Kazakh and Russian: please check every row, and especially the kk column (written best effort). Keys, placeholders and meaning are unchanged; tests that asserted an English string were updated to the new text.

Changed: 41 keys in 13 namespaces.

## Changed keys

| Key | Old (ru) | New (ru) | New (kk) | New (en) | Why |
| --- | --- | --- | --- | --- | --- |
| `about.district.none` | Список районов пока пуст. | Список районов скоро появится. | Аудандар тізімі жақында пайда болады. | The list of districts will appear soon. | Empty state says what will appear instead of 'the list is empty'. |
| `about.children.none` | Пока никого не добавили. | Здесь появятся ваши дети, когда вы их добавите. | Балаларыңызды қосқанда, олар осында көрінеді. | Your children will appear here once you add them. | Empty state: what will appear and how. |
| `assistant.errors.consent_required` | Чтобы пользоваться помощником, дайте согласие на трансграничный ИИ в разделе Конфиденциальность. | Помощник заработает, когда вы дадите согласие на трансграничный ИИ в разделе Конфиденциальность. | Құпиялылық бөлімінде шекарааралық ЖИ келісімін бергенде, көмекші жұмыс істейді. | The assistant will work once you give the cross-border AI consent in Privacy. | Invitation instead of a condition/order. |
| `assistant.errors.daily_limit` | Дневной лимит сообщений исчерпан. Попробуйте завтра. | На сегодня сообщения закончились. Завтра можно продолжить. | Бүгінгі хабарламалар таусылды. Ертең жалғастыруға болады. | That is all the messages for today. You can continue tomorrow. | 'Limit exhausted' is technical and cold; plain words and a next step. |
| `auth.errors.invalid_phone` | Введите правильный номер мобильного в международном формате. | Проверьте номер: он нужен в международном формате, например +7 700 000 00 00. | Нөмірді тексеріңіз: халықаралық формат керек, мысалы +7 700 000 00 00. | Please check the number: it needs the international format, for example +7 700 000 00 00. | 'Enter a correct number' blames the member; ask to check and show an example. |
| `auth.errors.rate_limited` | Слишком много попыток. Подождите минуту и попробуйте снова. | Давайте сделаем паузу. Через минуту можно попробовать снова. | Сәл үзіліс жасайық. Бір минуттан кейін қайталап көруге болады. | Let us take a short pause. You can try again in a minute. | 'Too many attempts' sounds like a reproach. |
| `auth.errors.invalid_code` | Неверный код. Проверьте SMS и попробуйте снова. | Код не подошёл. Проверьте SMS и попробуйте снова. | Код сәйкес келмеді. SMS-ті тексеріп, қайталап көріңіз. | That code did not match. Check the SMS and try again. | 'Wrong code' blames the member (guide example). |
| `auth.errors.flow_expired` | Срок действия кода истёк. Запросите новый. | Этот код уже устарел. Можно получить новый. | Бұл кодтың уақыты өтті. Жаңасын алуға болады. | This code has timed out. You can get a new one. | Bureaucratic wording; invitation instead of an order. |
| `auth.errors.network` | Нет подключения. Проверьте интернет и попробуйте снова. | Не получается подключиться. Проверьте интернет и попробуйте снова. | Қазір байланысу мүмкін болмай тұр. Интернетті тексеріп, қайталап көріңіз. | We cannot connect right now. Check the internet and try again. | Starts with a bare 'No'. |
| `chat.room.emptyDescription` | Поздоровайтесь. Будьте добры; жалобы уходят на модерацию. | Можно начать с приветствия. Здесь берегут друг друга, а модератор всегда рядом. | Сәлемдесуден бастауға болады. Мұнда бір-бірін аялайды, модератор әрқашан жанында. | You can start by saying hello. People here look after each other, and a moderator is always close by. | Order + warning about reports; invitation and reassurance, moderation still named. |
| `circle.tea.status.expired` | Истекло | Время вышло | Уақыты өтті | Time ran out | 'Expired' is technical. |
| `circle.tea.pref.sameDistrictOff` | Тот же район: нет | Тот же район: не важно | Сол аудан: маңызды емес | Same district: does not matter | 'no' reads as a refusal; the off state means 'not required'. |
| `circle.tea.pref.similarAgeOff` | Похожий возраст ребёнка: нет | Похожий возраст ребёнка: не важно | Бала жасы ұқсас: маңызды емес | Similar child age: does not matter | Same as sameDistrictOff. |
| `circle.tea.pref.sharedNeedsOff` | Общие потребности: нет | Общие потребности: не важно | Ортақ қажеттіліктер: маңызды емес | Shared needs: does not matter | Same as sameDistrictOff. |
| `circle.tea.errorConflict` | У вас уже есть открытая заявка. | Ваша заявка уже ждёт пары. Мы сообщим, когда найдём. | Өтініміңіз жұп күтіп тұр. Тапқанда хабарлаймыз. | Your request is already waiting for a match. We will let you know when we find someone. | Error without a next step; now reassures that the request is in progress. |
| `circle.tea.errorForbidden` | Чтобы попросить чай, нужна верификация. | Чай откроется после подтверждения. Попросить его можно в профиле. | Шай расталғаннан кейін ашылады. Растауды профильде сұрауға болады. | Tea opens once you are verified. You can ask for this in your profile. | Requirement phrased as a barrier; now what opens it and where (matches chat.rooms.unverifiedTitle). |
| `circle.matches.status.declined` | Отклонено | Не в этот раз | Бұл жолы емес | Not this time | 'Declined' reads as rejection of a person; mirrors the 'Not now' button. |
| `circle.matches.status.expired` | Истекло | Время вышло | Уақыты өтті | Time ran out | Same as tea.status.expired. |
| `home.checkin.off` | Напоминания о проверке самочувствия выключены. | Напоминания о проверке самочувствия выключены. Пройти её можно в любой момент. | Көңіл-күй тексеруі туралы еске салғыштар өшірулі. Тексеруден кез келген уақытта өтуге болады. | Weekly check-in reminders are off. You can check in any time. | Adds the next step. |
| `home.checkin.none` | Напоминание о проверке самочувствия ещё не настроено. | Напоминание пока не настроено. Пройти проверку можно в любой момент. | Еске салғыш әлі бапталмаған. Тексеруден кез келген уақытта өтуге болады. | No reminder set yet. You can check in any time. | Adds the next step. |
| `home.inbox.emptyTitle` | Пока ничего нового | Здесь пока тихо | Әзірге тыныш | All quiet for now | Owner's example of a red flag ('nothing new'). |
| `knowledge.topics.emptyTitle` | Нет тем | Темы скоро появятся | Тақырыптар жақында пайда болады | Topics coming soon | Bare 'No topics'. |
| `knowledge.topics.emptyDescription` | Опубликованные темы появятся здесь. | Каждый материал проверяют, прежде чем опубликовать. | Әр материалды жарияламас бұрын тексереді. | Every text is reviewed before it is published. | The title now says what will appear; the description explains why it takes time. |
| `knowledge.articles.emptyTitle` | Нет статей | Статьи скоро появятся | Мақалалар жақында пайда болады | Articles coming soon | Bare 'No articles'. |
| `knowledge.articles.emptyDescription` | Опубликованные статьи появятся здесь. | Каждый материал проверяют, прежде чем опубликовать. | Әр материалды жарияламас бұрын тексереді. | Every text is reviewed before it is published. | Same as topics.emptyDescription. |
| `knowledge.article.notFound` | Статья не найдена. | Эта статья сейчас недоступна. Её можно поискать среди всех тем. | Бұл мақала қазір қолжетімсіз. Оны барлық тақырыптардан іздеуге болады. | This article is not available right now. You can look for it in all topics. | 'Not found' with no next step. |
| `onboarding.welcome.body` | Бірге — место, где мамы поддерживают друг друга. Здесь можно спокойно рассказать, как вы, найти тех, кто понимает, и быстро получить помощь. Выберите язык, на нём будет всё приложение. | (unchanged) | (unchanged) | Birge is a place where mothers support each other. Here you can share how you are at your own pace, find people who understand, and get help quickly. Choose your language: the whole app will use it. | English only: 'Say how you are in peace' was unnatural; now matches ru/kk meaning. |
| `privacy.consents.emptyTitle` | Нет согласий | Пока нет согласий | Әзірге келісім жоқ | No consents yet | Bare 'No consents'; 'yet' matches other empty states. |
| `privacy.deletion.description` | Запросите удаление всех ваших данных. Они будут удалены в течение 24 часов. | Можно попросить удалить все ваши данные. Они будут удалены в течение 24 часов. | Барлық деректеріңізді жоюды сұрауға болады. Олар 24 сағат ішінде өшіріледі. | You can ask us to delete all your data. It will be removed within 24 hours. | Order -> invitation. |
| `privacy.deletion.sheetBody` | Это нельзя отменить. Аккаунт и все данные будут удалены в течение 24 часов. | После удаления вернуть данные не получится. Аккаунт и все данные будут удалены в течение 24 часов. | Жойылғаннан кейін деректерді қайтару мүмкін болмайды. Аккаунтыңыз бен барлық деректер 24 сағат ішінде жойылады. | Once deleted, your data cannot be restored. Your account and all data will be deleted within 24 hours. | 'This is forbidden to undo' -> honest consequence; the warning stays explicit. |
| `profile.verification.new` | Не подтверждён | Ещё не подтверждён | Әлі расталмаған | Not verified yet | 'yet' makes it a step, not a verdict. |
| `emergency.childCard.empty` | Карточки ребёнка пока нет. | Карточка ребёнка появится здесь, когда вы её сохраните. | Бала карточкасын сақтағанда, ол осында көрінеді. | Your child's card will appear here once you save it. | Empty state: what will appear and how. |
| `emergency.contacts.emptyDescription` | Добавьте до трёх людей, которым доверяете. Участницы должны быть знакомы лично и принять приглашение. | Добавьте до трёх людей, которым доверяете. Участница может стать контактом, если вы знакомы лично и она примет приглашение. | Сенетін үш адамға дейін қосыңыз. Қатысушы, егер сіз жеке таныс болсаңыз және ол шақыруды қабылдаса, байланыс бола алады. | Add up to three people you trust. A member can become a contact if you have met in person and she accepts the invitation. | 'Must' -> condition; the met-in-person rule (AGENTS.md non-negotiable 5) keeps its full meaning. |
| `emergency.contacts.invalidPhone` | Введите номер в международном формате. | Проверьте номер: он нужен в международном формате, например +70000000000. | Нөмірді тексеріңіз: халықаралық формат керек, мысалы +70000000000. | Please check the number: it needs the international format, for example +70000000000. | Same as auth.errors.invalid_phone. |
| `emergency.contacts.invalidName` | Введите имя. | Добавьте имя контакта. | Байланыстың атын қосыңыз. | Add the contact's name. | Softer than a bare order 'Enter a name'. |
| `emergency.contacts.full` | У вас уже три доверенных контакта. | У вас уже три доверенных контакта. Чтобы добавить нового, сначала уберите одного из списка. | Сізде үш сенімді байланыс бар. Жаңасын қосу үшін алдымен біреуін тізімнен алып тастаңыз. | You already have three trusted contacts. To add someone new, remove one from the list first. | Adds the next step. |
| `emergency.loadError` | Не удалось загрузить план. | Не удалось загрузить план. Попробуйте ещё раз чуть позже. | Жоспарды жүктеу мүмкін болмады. Сәлден кейін қайталап көріңіз. | Could not load the plan. Please try again in a moment. | Error without a next step. |
| `wellbeing.questions.missing` | Этот вопрос недоступен в этой версии приложения. | Этот вопрос появится после обновления приложения. | Бұл сұрақ қолданбаны жаңартқаннан кейін көрінеді. | This question will appear after you update the app. | Says what to do instead of 'not available'. Not an instrument item. |
| `wellbeing.suggestion.none` | Сейчас дополнительных шагов нет. | Сейчас ничего делать не нужно. Берегите себя. | Қазір басқа ештеңе істеудің қажеті жоқ. Өзіңізді күтіңіз. | Nothing more to do right now. Take care of yourself. | 'No extra steps' is cold. Not a scoring text (not referenced by the questionnaire preset); psychologist to confirm. |
| `admin.knowledge.problem.forbidden` | Вам нельзя выполнить это действие. Может понадобиться другой рецензент, или вы автор этой версии. | Это действие доступно другой роли. Возможно, нужен другой рецензент, или вы автор этой версии. | Бұл әрекет басқа рөлге қолжетімді. Басқа шолушы керек болуы мүмкін, немесе сіз осы нұсқаның авторысыз. | This action belongs to another role. Another reviewer may be needed, or you authored this version. | Staff console: 'you are not allowed' -> neutral reason. |
| `admin.knowledge.problem.conflict` | Эту версию нельзя изменить в текущем состоянии. | Эту версию сейчас не получится изменить: её статус уже другой. Обновите список. | Бұл нұсқаны қазір өзгерту мүмкін емес: оның күйі өзгерген. Тізімді жаңартыңыз. | This version cannot be changed right now: its status has changed. Refresh the list. | Staff console: adds the reason and a next step. |

## Not changed on purpose

These texts are excluded from tone edits (see [content-style.md](content-style.md#exclusions-never-edited-for-tone)). Nothing below was edited; the concerns are for the psychologist and native speakers to decide.

### Wellbeing questionnaire (validated instrument)
Keys: `wellbeing.phq4.q1`–`q4`, `wellbeing.phq4.opt.0`–`3`, `wellbeing.pss4.q1`–`q4`, `wellbeing.pss4.opt.0`–`4`, the scoring texts `wellbeing.feedback.none|elevated|crisis|missing` (referenced as `feedback_key` in `presets/demo/questionnaire.json`), `wellbeing.history.point`.

Concerns:
- The ru and kk item wordings look like working translations. Before release they should be replaced by the published validated Russian and Kazakh versions of PHQ-4 and PSS-4 (if a validated Kazakh version does not exist, the psychologist decides how to proceed). `pss4.q2` «личными проблемами» / "personal problems" is the instrument's wording and stays.
- kk `wellbeing.pss4.opt.3` «Жиілеу» ("Fairly often") may read unnaturally; please compare with the validated scale.
- kk `wellbeing.feedback.none` and `feedback.missing` «Тексерілгеніңіз үшін рақмет» can read as "thank you for being examined"; «Тексеруден өткеніңіз үшін рақмет» may be closer. Scoring text, so not changed.
- `wellbeing.start.intro` «Это не диагноз.» was kept: it reassures rather than labels. The psychologist may prefer «Это не медицинская оценка».

### Escalation to the psychologist after a check-in
Keys: `wellbeing.elevated.*`. Kept as is because it is part of the crisis path and mirrors `help.status.*`.
- `wellbeing.elevated.queued` starts with «Нет подключения.» / «Байланыс жоқ.» / "No connection." (the same text as `help.status.queued`). A softer start such as «Сейчас нет интернета. Запрос сохранён…» could be considered for both together, through the crisis-path review.

### Crisis texts
Keys: all of `help.*` and `sos.*`; `chat.room.held`, `chat.room.crisisAction`; `assistant.crisis.*`; `assistant.degraded.*`.
- `sos.status.rateLimited` «Слишком много SOS. Подождите немного и попробуйте снова.» is the one text that worries us: a person in crisis who sees "too many" may feel blamed or give up. Suggest something like «SOS уже отправлен, его видят модераторы и доверенные контакты. Если нужна помощь прямо сейчас, звоните 112.», with the same in kk and en. Needs the crisis-path review.
- `help.status.queued`, `sos.status.queued` start with «Нет подключения» (see above).
- `help.status.signedOut`, `sos.status.signedOut` («нужно войти» / «кіру керек») are direct, and they say that phones and 112 work without signing in; kept.
- kk `chat.room.held` «тексеруге ұсталды» can read as "detained for checking"; a native speaker may prefer «тексеруге жіберілді».
- `sos.types.feelBad` «Мне очень плохо» is the member's own voice and fits; kept.

### Legal texts
`presets/demo/knowledge/articles.json` (topics under "My rights") contains only DEMO placeholders. Their titles use the legal term «пособие по инвалидности» / «мүгедектік жәрдемақысы» / "disability benefit", which is correct for a legal text. The real knowledge base is reviewed by its own editors.

### Required disclosure
`assistant.intro` «Я бот и ИИ-помощник, а не врач и не психолог.» stays explicit.

### Other texts seen and left alone (suggestions only)
- `emergency.title`, `profile.emergencyPlan` «План ЧС» is bureaucratic; «Экстренный план» or «План на экстренный случай» (as in `inbox.templates.emergency.plan_sent`) may be warmer. Not changed: it is a screen title and needs a design check for length.
- `profile.verification.why` «убедится, что вы мама из нашего сообщества» may sound like suspicion; «познакомится с вами» could be gentler but changes what the call is for.
- Moderation and report wording (`chat.room.report*`, `chat.room.reasons.*`) and the staff console (`admin.*`, apart from two error texts) are for staff or describe someone else's behaviour; kept.
- Crisis lexicon and fixed crisis replies in `presets/demo/crisis/` and SMS texts in `presets/demo/sms_texts.json` are outside the locale files and were not audited.
