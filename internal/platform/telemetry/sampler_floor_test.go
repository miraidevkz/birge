package telemetry

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUUIDV7FloorSortsBelowLaterIDs(t *testing.T) {
	at := time.Now().Add(-time.Hour)
	floor := uuidV7Floor(at)
	if floor.Version() != 7 {
		t.Fatalf("version %d", floor.Version())
	}
	later, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	if string(later[:]) < string(floor[:]) {
		t.Errorf("a uuidv7 made after the floor sorts below it: %s < %s", later, floor)
	}
	if earlier := uuidV7Floor(at.Add(-time.Millisecond)); string(earlier[:]) >= string(floor[:]) {
		t.Errorf("floor of an earlier time does not sort below: %s >= %s", earlier, floor)
	}
}
