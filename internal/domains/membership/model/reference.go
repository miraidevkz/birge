package model

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ReferenceNamespace is the fixed UUIDv5 namespace of reference IDs. Never
// change it: the IDs derived from it are stored in members, rooms, cards and
// listings, and every deployment derives the same ID for the same code.
var ReferenceNamespace = uuid.MustParse("fa11162a-fe71-47b2-8186-2a1c844206e7")

// ErrInvalidReference rejects reference data before the sync touches the
// database. It is an operator error (a bad preset file), never a member's.
var ErrInvalidReference = errors.New("membership: invalid reference data")

// CityID is the stable ID of a city created by the reference sync.
func CityID(code string) uuid.UUID {
	return uuid.NewSHA1(ReferenceNamespace, []byte("city/"+code))
}

// DistrictID is the stable ID of a district created by the reference sync.
// The city code is part of the name because the database key is (city, code).
func DistrictID(cityCode, code string) uuid.UUID {
	return uuid.NewSHA1(ReferenceNamespace, []byte("district/"+cityCode+"/"+code))
}

// ReferenceCitySeed is a city code with its district codes.
type ReferenceCitySeed struct {
	Code      string
	Districts []string
}

// ReferenceSeed is the complete reference data a deployment offers: the sync
// makes exactly these entries active. Labels are not stored (locale files).
type ReferenceSeed struct {
	Cities []ReferenceCitySeed
	Needs  []string
}

// Validate checks the codes and their uniqueness. District codes must be
// unique across cities, not only within one, because clients label them by
// code alone (locale key district.<code>, room key chat.room.district.<code>).
func (r ReferenceSeed) Validate() error {
	if len(r.Cities) == 0 || len(r.Needs) == 0 {
		return fmt.Errorf("%w: reference needs cities and needs", ErrInvalidReference)
	}
	cities, districts, needs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, c := range r.Cities {
		if !codeRe.MatchString(c.Code) || len(c.Districts) == 0 {
			return fmt.Errorf("%w: reference city %q needs a valid code and districts", ErrInvalidReference, c.Code)
		}
		if cities[c.Code] {
			return fmt.Errorf("%w: duplicate reference city %q", ErrInvalidReference, c.Code)
		}
		cities[c.Code] = true
		for _, d := range c.Districts {
			if !codeRe.MatchString(d) {
				return fmt.Errorf("%w: reference district %q has an invalid code", ErrInvalidReference, d)
			}
			if districts[d] {
				return fmt.Errorf("%w: duplicate reference district %q", ErrInvalidReference, d)
			}
			districts[d] = true
		}
	}
	for _, n := range r.Needs {
		if !codeRe.MatchString(n) {
			return fmt.Errorf("%w: reference need %q has an invalid code", ErrInvalidReference, n)
		}
		if needs[n] {
			return fmt.Errorf("%w: duplicate reference need %q", ErrInvalidReference, n)
		}
		needs[n] = true
	}
	return nil
}

// ReferenceSyncResult counts what one reference sync changed. All zero means
// the database already matched the seed.
type ReferenceSyncResult struct {
	Inserted    int
	Reactivated int
	Deactivated int
}

// Changed reports whether the sync changed any row.
func (r ReferenceSyncResult) Changed() bool {
	return r.Inserted+r.Reactivated+r.Deactivated > 0
}
