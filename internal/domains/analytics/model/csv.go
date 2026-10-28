package model

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// CSVHeader is the header row of the export (contract getMetricsCsv).
var CSVHeader = []string{"metric", "bucket", "members", "value", "suppressed"}

// CSVFilename is the attachment name of the export for p.
func CSVFilename(p Period) string {
	return fmt.Sprintf("birge-metrics-%s-%s.csv", p.From.Format(time.DateOnly), p.To.Format(time.DateOnly))
}

// Row is one CSV row before formatting: a metric (contract field path), its
// bucket (a date for weekly and monthly series, empty otherwise) and its cell.
type Row struct {
	Metric string
	Bucket string
	Cell   Cell
}

// Rows lists m as CSV rows in report order. m must already be suppressed.
func Rows(m Metrics) []Row {
	rows := []Row{
		{"stress.baseline.mean", "", m.StressBaseline.Mean},
		{"stress.baseline.median", "", m.StressBaseline.Median},
		{"stress.week8.mean", "", m.StressWeek8.Mean},
		{"stress.week8.median", "", m.StressWeek8.Median},
		{"isolation.before.mean", "", m.IsolationBefore.Mean},
		{"isolation.before.median", "", m.IsolationBefore.Median},
		{"isolation.after.mean", "", m.IsolationAfter.Mean},
		{"isolation.after.median", "", m.IsolationAfter.Median},
	}
	for _, b := range m.ActiveWeekly {
		rows = append(rows, Row{"activity.weekly", b.Start.Format(time.DateOnly), b.Cell})
	}
	for _, b := range m.ActiveMonthly {
		rows = append(rows, Row{"activity.monthly", b.Start.Format(time.DateOnly), b.Cell})
	}
	return append(rows,
		Row{"activity.retention4w", "", m.Retention4w},
		Row{"activity.retention8w", "", m.Retention8w},
		Row{"tea.requests", "", m.TeaRequests},
		Row{"tea.pairs", "", m.TeaPairs},
		Row{"tea.meetings", "", m.TeaMeetings},
		Row{"sos.raised", "", m.SosRaised},
		Row{"sos.closedShare", "", m.SosClosedShare},
		Row{"sos.firstResponseMedianMinutes", "", m.SosFirstResponseMedianMinutes},
		Row{"escalations.opened", "", m.EscalationsOpened},
		Row{"escalations.takenWithin15mShare", "", m.EscalationsTakenWithin15mShare},
		Row{"rights.answeredFromKnowledge", "", m.RightsAnsweredFromKnowledge},
		Row{"rights.helpedShare", "", m.RightsHelpedShare},
	)
}

// WriteCSV writes m (already suppressed) as UTF-8 CSV with a header row. A
// suppressed row has empty members and value. Every field passes through
// csvSafe, although none of ours can start with a formula character.
func WriteCSV(w io.Writer, m Metrics) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(CSVHeader); err != nil {
		return fmt.Errorf("analytics: csv header: %w", err)
	}
	for _, r := range Rows(m) {
		members, value := "", ""
		if !r.Cell.Suppressed {
			members = strconv.Itoa(r.Cell.Members)
			value = FormatValue(r.Cell.Value)
		}
		rec := []string{r.Metric, r.Bucket, members, value, strconv.FormatBool(r.Cell.Suppressed)}
		for i := range rec {
			rec[i] = csvSafe(rec[i])
		}
		if err := cw.Write(rec); err != nil {
			return fmt.Errorf("analytics: csv row: %w", err)
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("analytics: csv flush: %w", err)
	}
	return nil
}

// FormatValue prints a value with the precision the JSON response carries
// (float32), so the CSV and the JSON agree.
func FormatValue(v float64) string {
	return strconv.FormatFloat(float64(float32(v)), 'f', -1, 32)
}

// csvSafe neutralises spreadsheet formula injection (OWASP): a field that
// starts with = + - @, a tab or a carriage return gets a leading apostrophe.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
