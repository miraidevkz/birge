package telemetry

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.yaml.in/yaml/v3"
)

const alertsFile = "../../../deploy/prometheus/alerts.yml"

type ruleFile struct {
	Groups []struct {
		Name  string `yaml:"name"`
		Rules []struct {
			Alert       string            `yaml:"alert"`
			Expr        string            `yaml:"expr"`
			For         string            `yaml:"for"`
			Labels      map[string]string `yaml:"labels"`
			Annotations map[string]string `yaml:"annotations"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

// registeredNames returns the metric names of every Birge collector.
func registeredNames(t *testing.T) map[string]bool {
	t.Helper()
	fqName := regexp.MustCompile(`fqName: "([a-z_]+)"`)
	names := map[string]bool{}
	ch := make(chan *prometheus.Desc, 64)
	for _, c := range birgeCollectors {
		go func() { c.Describe(ch); ch <- nil }()
		for d := <-ch; d != nil; d = <-ch {
			if m := fqName.FindStringSubmatch(d.String()); m != nil {
				names[m[1]] = true
			}
		}
	}
	return names
}

// TestAlertRules is the structural lint of deploy/prometheus/alerts.yml that
// runs in every `make check` (promtool is run by hand, docs/development.md):
// every rule is named, has a severity and a summary, and every Birge metric
// it names is registered here, so a renamed metric cannot silently disable
// an alert.
func TestAlertRules(t *testing.T) {
	raw, err := os.ReadFile(alertsFile)
	if err != nil {
		t.Fatal(err)
	}
	var f ruleFile
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("parse %s: %v", alertsFile, err)
	}
	names := registeredNames(t)
	metricRe := regexp.MustCompile(`\bbirge_[a-z_]+\b`)
	seen := map[string]bool{}
	required := map[string]bool{
		"BirgeTargetDown": false, "BirgeCrisisPathErrors": false, "BirgeEscalationUntaken": false,
		"BirgeTimerOverdue": false, "BirgeOutboxLag": false, "BirgeSMSSpike": false, "BirgeLLMErrorRate": false,
	}
	for _, g := range f.Groups {
		if g.Name == "" || len(g.Rules) == 0 {
			t.Errorf("group %q is empty or unnamed", g.Name)
		}
		for _, r := range g.Rules {
			if r.Alert == "" || seen[r.Alert] {
				t.Errorf("rule %q: missing or duplicate alert name", r.Alert)
			}
			seen[r.Alert] = true
			if _, ok := required[r.Alert]; ok {
				required[r.Alert] = true
			}
			if s := r.Labels["severity"]; s != "critical" && s != "warning" {
				t.Errorf("%s: severity %q, want critical or warning", r.Alert, s)
			}
			if r.Annotations["summary"] == "" {
				t.Errorf("%s: no summary", r.Alert)
			}
			if r.For != "" {
				if _, err := time.ParseDuration(r.For); err != nil {
					t.Errorf("%s: for %q: %v", r.Alert, r.For, err)
				}
			}
			if strings.Count(r.Expr, "(") != strings.Count(r.Expr, ")") || strings.TrimSpace(r.Expr) == "" {
				t.Errorf("%s: malformed expr %q", r.Alert, r.Expr)
			}
			for _, m := range metricRe.FindAllString(r.Expr, -1) {
				base := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(m, "_bucket"), "_count"), "_sum")
				if !names[m] && !names[base] {
					t.Errorf("%s: metric %s is not registered in telemetry", r.Alert, m)
				}
			}
		}
	}
	for name, found := range required {
		if !found {
			t.Errorf("required alert %s (ADR-0021) missing", name)
		}
	}
}

// TestMetricLabelsHoldNoIDs keeps IDs out of labels: no Birge metric may
// have a label whose name says it holds an identifier or personal data.
func TestMetricLabelsHoldNoIDs(t *testing.T) {
	labelsRe := regexp.MustCompile(`variableLabels: \{([^}]*)\}`)
	ch := make(chan *prometheus.Desc, 64)
	for _, c := range birgeCollectors {
		go func() { c.Describe(ch); ch <- nil }()
		for d := <-ch; d != nil; d = <-ch {
			m := labelsRe.FindStringSubmatch(d.String())
			if m == nil {
				continue
			}
			for _, l := range strings.Split(m[1], ",") {
				l = strings.TrimSpace(l)
				if strings.HasSuffix(l, "_id") || l == "id" || l == "member" || l == "phone" || l == "path" || l == "url" {
					t.Errorf("%s has label %q", d, l)
				}
			}
		}
	}
}
