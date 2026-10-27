package anonymize_test

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"birge/internal/domains/assistant/anonymize"
)

func testDictionary(t *testing.T) []string {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "names.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	names, err := anonymize.ParseNameList(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("empty test dictionary")
	}
	return names
}

func TestAnonymize(t *testing.T) {
	a := anonymize.New(testDictionary(t))
	tests := []struct {
		name, in, own, want string
	}{
		{"phone plus seven", "звоните +7 701 123 45 67 вечером", "", "звоните [PHONE] вечером"},
		{"phone brackets", "8 (701) 123-45-67", "", "[PHONE]"},
		{"phone compact", "87011234567", "", "[PHONE]"},
		{"phone ten digits", "701 123 45 67", "", "[PHONE]"},
		{"phone local", "тел 55-66-77", "", "тел [PHONE]"},
		{"iin", "ИИН 900101300123.", "", "ИИН [IIN]."},
		{"iin spaced", "ИИН 900101 300123", "", "ИИН [IIN]"},
		{"email", "пишите test.user@example.kz!", "", "пишите [EMAIL]!"},
		{"email spaced", "test @ example.com", "", "[EMAIL]"},
		{"street ru", "живём на ул. Абая 15, кв. 3, рядом школа", "", "живём на [ADDRESS], рядом школа"},
		{"street ru full", "улица Сатпаева, д. 22", "", "[ADDRESS]"},
		{"microdistrict", "мкр. Самал-2, дом 5", "", "[ADDRESS]"},
		{"street kk", "Абай көшесі 15, пәтер 8", "", "[ADDRESS]"},
		{"street kk russian keyboard", "Абай кошеси 15", "", "[ADDRESS]"},
		{"street en", "15 Abay Street", "", "[ADDRESS]"},
		{"flat only", "дом 7 кв. 45", "", "[ADDRESS]"},
		{"word like street abbreviation", "улыбка 5 раз", "", "улыбка 5 раз"},
		{"passport", "паспорт N12345678", "", "паспорт [DOC]"},
		{"document digits", "удостоверение 045123456", "", "удостоверение [DOC]"},
		{"date kept", "комиссия 12.05.2026 в 10:00", "", "комиссия 12.05.2026 в 10:00"},
		{"amount kept", "пособие 150 000 тенге", "", "пособие 150 000 тенге"},
		{"small numbers kept", "сыну 7 лет, 2 класс", "", "сыну 7 лет, 2 класс"},
		{"dictionary ru nominative", "Мне помогла Анна.", "", "Мне помогла [NAME]."},
		{"dictionary ru instrumental", "говорила с Анной", "", "говорила с [NAME]"},
		{"dictionary ru genitive -й", "у Сергея", "", "у [NAME]"},
		{"dictionary ru dative consonant", "Арману пять", "", "[NAME] пять"},
		{"dictionary lowercase", "арман не спит", "", "[NAME] не спит"},
		{"dictionary kk genitive", "Арманның дәрігері", "", "[NAME] дәрігері"},
		{"dictionary kk dative", "Арманға жеті жас", "", "[NAME] жеті жас"},
		{"dictionary kk instrumental", "Айгүлмен сөйлестім", "", "[NAME] сөйлестім"},
		{"dictionary kk possessive chain", "Арманымның мектебі", "", "[NAME] мектебі"},
		{"dictionary kk russian keyboard", "Арманнын", "", "[NAME]"},
		{"own name forms ru", "Зарины нет дома, Зарине плохо", "Зарина Тестова", "[NAME] нет дома, [NAME] плохо"},
		{"own surname", "я Тестова", "Зарина Тестова", "я [NAME]"},
		{"own name kk", "Заринаға айтыңыз", "Зарина", "[NAME] айтыңыз"},
		{"own short name", "Ая устала", "Ая", "[NAME] устала"},
		{"english possessive", "David's school", "", "[NAME]'s school"},
		{"label not renamed", "[PHONE] and [NAME]", "", "[PHONE] and [NAME]"},
		{"empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var own []string
			if tt.own != "" {
				own = []string{tt.own}
			}
			if got := a.Anonymize(tt.in, own...); got != tt.want {
				t.Fatalf("Anonymize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNilAnonymizerStillMasksPatterns(t *testing.T) {
	var a *anonymize.Anonymizer
	got := a.Anonymize("Анна +77011234567", "Анна")
	if got != "[NAME] [PHONE]" {
		t.Fatalf("got %q", got)
	}
}

// Independent detectors for the leak check: any run of ten or more digits
// (phones, IIN, documents) and anything that looks like an e-mail.
var (
	leakDigitsRe = regexp.MustCompile(`\d(?:[\s().\-]*\d){9,}`)
	leakEmailRe  = regexp.MustCompile(`@\s*[\p{L}\p{N}\-]+\.`)
)

// TestAnonymizationEvalSet runs evals/datasets/anonymization/{kk,ru,en}.tsv
// (ADR-0017: zero leaks). Every line's must-not-contain must be gone, and no
// phone, IIN or e-mail may survive on any line.
func TestAnonymizationEvalSet(t *testing.T) {
	a := anonymize.New(testDictionary(t))
	dir := filepath.Join("..", "..", "..", "..", "evals", "datasets", "anonymization")
	for _, lang := range []string{"kk", "ru", "en"} {
		t.Run(lang, func(t *testing.T) {
			f, err := os.Open(filepath.Join(dir, lang+".tsv"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			sc := bufio.NewScanner(f)
			cases, line := 0, 0
			for sc.Scan() {
				line++
				text := sc.Text()
				if strings.TrimSpace(text) == "" || strings.HasPrefix(text, "#") {
					continue
				}
				input, mustNot, ok := strings.Cut(text, "\t")
				if !ok || input == "" || mustNot == "" || strings.Contains(mustNot, "\t") {
					t.Fatalf("%s.tsv:%d: want input<TAB>must-not-contain", lang, line)
				}
				cases++
				got := a.Anonymize(input)
				// Report the line number only: the case text stays in the dataset.
				if strings.Contains(got, mustNot) {
					t.Errorf("%s.tsv:%d: must-not-contain survived", lang, line)
				}
				if leakDigitsRe.MatchString(got) || leakEmailRe.MatchString(got) {
					t.Errorf("%s.tsv:%d: phone, IIN or e-mail survived", lang, line)
				}
			}
			if err := sc.Err(); err != nil {
				t.Fatal(err)
			}
			if cases < 10 {
				t.Fatalf("%s.tsv: only %d cases", lang, cases)
			}
		})
	}
}
