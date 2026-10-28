package presets_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	membershipmodel "birge/internal/domains/membership/model"
	"birge/presets"
)

// TestReferenceSetsLoad checks every reference set: names in kk, ru and en
// for every entry (LoadReference refuses a missing one; checked again here so
// a weakened loader cannot pass), valid codes, and Kazakh names in Cyrillic.
func TestReferenceSetsLoad(t *testing.T) {
	for _, name := range presets.ReferenceSets {
		t.Run(name, func(t *testing.T) {
			ref, err := presets.LoadReference(name)
			if err != nil {
				t.Fatal(err)
			}
			check := func(kind, code string, names map[string]string) {
				for _, lang := range presets.ReferenceLangs {
					if strings.TrimSpace(names[lang]) == "" {
						t.Errorf("%s %s: missing %s name", kind, code, lang)
					}
				}
				if len(names) != len(presets.ReferenceLangs) {
					t.Errorf("%s %s: names in unexpected languages: %v", kind, code, names)
				}
				if !strings.ContainsFunc(names["kk"], isCyrillic) {
					t.Errorf("%s %s: kk name %q is not Cyrillic", kind, code, names["kk"])
				}
			}
			for _, c := range ref.Cities {
				check("city", c.Code, c.Names)
				for _, d := range c.Districts {
					check("district", d.Code, d.Names)
				}
			}
			for _, n := range ref.Needs {
				check("need", n.Code, n.Names)
			}
		})
	}
	if _, err := presets.LoadReference("mars"); err == nil {
		t.Error("unknown set loaded")
	}
}

func isCyrillic(r rune) bool { return r >= 0x0400 && r <= 0x04FF }

// TestReferenceCodesAndIDsUnique checks that codes are unique across all
// sets (clients label districts by code alone, and migrate may sync several
// sets at once) and that the stable IDs derived from them never collide.
func TestReferenceCodesAndIDsUnique(t *testing.T) {
	cities, districts := map[string]string{}, map[string]string{}
	ids := map[uuid.UUID]string{}
	addID := func(id uuid.UUID, what string) {
		if prev, dup := ids[id]; dup {
			t.Errorf("stable ID of %s collides with %s", what, prev)
		}
		ids[id] = what
	}
	for _, name := range presets.ReferenceSets {
		ref, err := presets.LoadReference(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range ref.Cities {
			if prev, dup := cities[c.Code]; dup {
				t.Errorf("city %s in sets %s and %s", c.Code, prev, name)
			}
			cities[c.Code] = name
			addID(membershipmodel.CityID(c.Code), "city "+c.Code)
			for _, d := range c.Districts {
				if prev, dup := districts[d.Code]; dup {
					t.Errorf("district %s in sets %s and %s", d.Code, prev, name)
				}
				districts[d.Code] = name
				addID(membershipmodel.DistrictID(c.Code, d.Code), "district "+d.Code)
			}
		}
	}
	// The derivation itself is fixed: changing the namespace or the name
	// format would give every deployment new IDs on the next migrate.
	if got := membershipmodel.DistrictID("almaty", "almaty_bostandyk"); got != uuid.NewSHA1(membershipmodel.ReferenceNamespace, []byte("district/almaty/almaty_bostandyk")) {
		t.Errorf("district id derivation changed: %v", got)
	}
	if membershipmodel.ReferenceNamespace.String() != "fa11162a-fe71-47b2-8186-2a1c844206e7" {
		t.Error("reference namespace changed")
	}
}

// TestReferenceKZContent pins the Kazakhstan set to the administrative
// division as of 2026 and the DEMO set to the codes the demo stand's rows
// carry (the sync matches existing rows by code).
func TestReferenceKZContent(t *testing.T) {
	kz, err := presets.LoadReference(presets.ReferenceKZ)
	if err != nil {
		t.Fatal(err)
	}
	// 3 cities of republican significance + 17 regional centres.
	if len(kz.Cities) != 20 {
		t.Errorf("kz cities = %d, want 20", len(kz.Cities))
	}
	wantDistricts := map[string]int{"almaty": 8, "astana": 6, "shymkent": 5}
	for _, c := range kz.Cities {
		want, ok := wantDistricts[c.Code]
		if !ok {
			want = 1 // a regional centre: one whole-city entry
		}
		if len(c.Districts) != want {
			t.Errorf("city %s districts = %d, want %d", c.Code, len(c.Districts), want)
		}
		for _, d := range c.Districts {
			if !strings.HasPrefix(d.Code, c.Code+"_") {
				t.Errorf("district %s does not carry its city prefix %s_", d.Code, c.Code)
			}
		}
	}

	demo, err := presets.LoadReference(presets.ReferenceDemo)
	if err != nil {
		t.Fatal(err)
	}
	if len(demo.Cities) != 1 || demo.Cities[0].Code != "demo_city" ||
		!slices.Equal(demo.DistrictCodes(), []string{"demo_district_a", "demo_district_b", "demo_district_c"}) {
		t.Errorf("DEMO codes changed: %+v", demo)
	}
	if !slices.Equal(kz.NeedCodes(), demo.NeedCodes()) {
		t.Errorf("kz needs %v, want the DEMO list %v", kz.NeedCodes(), demo.NeedCodes())
	}
}

// TestReferenceLabelsInClientLocales checks that every reference code has a
// label in the client locale files: the API returns codes only, and a code
// without a label is shown raw in the apps.
func TestReferenceLabelsInClientLocales(t *testing.T) {
	for _, lang := range presets.ReferenceLangs {
		raw, err := os.ReadFile(filepath.Join("..", "packages", "i18n", "src", "locales", lang, "reference.json"))
		if err != nil {
			t.Fatal(err)
		}
		var labels map[string]json.RawMessage
		if err := json.Unmarshal(raw, &labels); err != nil {
			t.Fatal(err)
		}
		section := func(kind string) map[string]string {
			var m map[string]string
			if err := json.Unmarshal(labels[kind], &m); err != nil {
				t.Fatalf("%s %s: %v", lang, kind, err)
			}
			return m
		}
		cities, districts, needs := section("city"), section("district"), section("need")
		for _, name := range presets.ReferenceSets {
			ref, err := presets.LoadReference(name)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range ref.Cities {
				if cities[c.Code] == "" {
					t.Errorf("%s: no label for city %s", lang, c.Code)
				}
				for _, d := range c.Districts {
					if districts[d.Code] == "" {
						t.Errorf("%s: no label for district %s", lang, d.Code)
					}
				}
			}
			for _, n := range ref.Needs {
				if needs[n.Code] == "" {
					t.Errorf("%s: no label for need %s", lang, n.Code)
				}
			}
		}
	}
}
