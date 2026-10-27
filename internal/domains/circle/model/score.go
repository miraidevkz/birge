package model

import (
	"bytes"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
)

// BirthYears returns the birth years of p's children.
func BirthYears(p Profile) []int {
	out := make([]int, 0, len(p.Children))
	for _, c := range p.Children {
		out = append(out, c.BirthYear)
	}
	return out
}

// AllNeeds returns the de-duplicated need codes across p's children.
func AllNeeds(p Profile) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, c := range p.Children {
		for _, n := range c.Needs {
			if n == "" {
				continue
			}
			if _, ok := seen[n]; ok {
				continue
			}
			seen[n] = struct{}{}
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// SharedNeeds returns the sorted intersection of need codes.
func SharedNeeds(a, b Profile) []string {
	set := map[string]struct{}{}
	for _, n := range AllNeeds(a) {
		set[n] = struct{}{}
	}
	var out []string
	for _, n := range AllNeeds(b) {
		if _, ok := set[n]; ok {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// ChildAges returns ages in whole years at now for each child (0 when birth
// year is in the future). Used for match cards only.
func ChildAges(p Profile, now time.Time) []int {
	year := now.UTC().Year()
	out := make([]int, 0, len(p.Children))
	for _, c := range p.Children {
		age := year - c.BirthYear
		if age < 0 {
			age = 0
		}
		out = append(out, age)
	}
	return out
}

// AgesWithin reports whether any child of a is within ±window years of any
// child of b (by birth year).
func AgesWithin(a, b Profile, window int) bool {
	ya := BirthYears(a)
	yb := BirthYears(b)
	if len(ya) == 0 || len(yb) == 0 {
		return false
	}
	for _, x := range ya {
		for _, y := range yb {
			d := x - y
			if d < 0 {
				d = -d
			}
			if d <= window {
				return true
			}
		}
	}
	return false
}

// SameDistrict reports whether both profiles have the same non-nil district.
func SameDistrict(a, b Profile) bool {
	return a.DistrictID != nil && b.DistrictID != nil && *a.DistrictID == *b.DistrictID
}

// ScorePair returns the match score for a and b, or ok=false when they are
// incompatible (different format or a hard preference fails). Blocked and
// rematch filters are applied by the service before calling this.
func ScorePair(a, b Candidate, w ScoreWeights) (int, bool) {
	if a.Request.Format != b.Request.Format {
		return 0, false
	}
	sameDistrict := SameDistrict(a.Profile, b.Profile)
	if (a.Request.Preferences.SameDistrict || b.Request.Preferences.SameDistrict) && !sameDistrict {
		return 0, false
	}
	ageClose := AgesWithin(a.Profile, b.Profile, AgeWindowYears)
	if (a.Request.Preferences.SimilarAge || b.Request.Preferences.SimilarAge) && !ageClose {
		return 0, false
	}
	shared := SharedNeeds(a.Profile, b.Profile)
	if (a.Request.Preferences.SharedNeeds || b.Request.Preferences.SharedNeeds) && len(shared) == 0 {
		return 0, false
	}

	w = w.Normalize()
	score := 0
	if sameDistrict {
		score += w.District
	}
	if ageClose {
		score += w.Age
	}
	if len(shared) > 0 {
		score += w.Need
	}
	if a.Profile.Lang != "" && a.Profile.Lang == b.Profile.Lang {
		score += w.Language
	}
	return score, true
}

// PairOpenRequests greedily pairs candidates by descending score. Tie-break:
// earlier of the two request created_at, then smaller of the two request ids.
// used marks request ids already paired; callers pass an empty set or one
// pre-seeded to exclude members. blocked(a,b) and recentlyMatched(a,b) reject
// a pair when true; an error from either aborts.
func PairOpenRequests(
	cands []Candidate,
	w ScoreWeights,
	blocked func(a, b uuid.UUID) (bool, error),
	recentlyMatched func(a, b uuid.UUID) (bool, error),
) ([]PairScore, error) {
	type edge struct {
		i, j  int
		score int
	}
	var edges []edge
	for i := 0; i < len(cands); i++ {
		for j := i + 1; j < len(cands); j++ {
			if cands[i].Request.MemberID == cands[j].Request.MemberID {
				continue
			}
			score, ok := ScorePair(cands[i], cands[j], w)
			if !ok {
				continue
			}
			lo, hi := CanonicalPair(cands[i].Request.MemberID, cands[j].Request.MemberID)
			if blocked != nil {
				b, err := blocked(lo, hi)
				if err != nil {
					return nil, err
				}
				if b {
					continue
				}
			}
			if recentlyMatched != nil {
				r, err := recentlyMatched(lo, hi)
				if err != nil {
					return nil, err
				}
				if r {
					continue
				}
			}
			edges = append(edges, edge{i: i, j: j, score: score})
		}
	}
	sort.SliceStable(edges, func(p, q int) bool {
		ep, eq := edges[p], edges[q]
		if ep.score != eq.score {
			return ep.score > eq.score
		}
		ap, bp := cands[ep.i].Request, cands[ep.j].Request
		aq, bq := cands[eq.i].Request, cands[eq.j].Request
		minCreated := func(x, y TeaRequest) time.Time {
			if x.CreatedAt.Before(y.CreatedAt) {
				return x.CreatedAt
			}
			return y.CreatedAt
		}
		cp, cq := minCreated(ap, bp), minCreated(aq, bq)
		if !cp.Equal(cq) {
			return cp.Before(cq)
		}
		minID := func(x, y TeaRequest) uuid.UUID {
			if bytesLess(x.ID, y.ID) {
				return x.ID
			}
			return y.ID
		}
		return bytesLess(minID(ap, bp), minID(aq, bq))
	})

	used := map[uuid.UUID]struct{}{}
	var out []PairScore
	for _, e := range edges {
		a, b := cands[e.i], cands[e.j]
		if _, ok := used[a.Request.ID]; ok {
			continue
		}
		if _, ok := used[b.Request.ID]; ok {
			continue
		}
		used[a.Request.ID] = struct{}{}
		used[b.Request.ID] = struct{}{}
		out = append(out, PairScore{A: a, B: b, Score: e.score})
	}
	return out, nil
}

func bytesLess(a, b uuid.UUID) bool {
	return bytes.Compare(a[:], b[:]) < 0
}
