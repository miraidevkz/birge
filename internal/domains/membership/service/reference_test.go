package service_test

import (
	"testing"

	"github.com/google/uuid"

	"birge/internal/domains/membership/model"
)

// TestDeactivatedDistrictKeptByMember checks the reference sync rule in the
// service: a deactivated district cannot be chosen anew, while a member who
// already has it keeps it through later profile updates.
func TestDeactivatedDistrictKeptByMember(t *testing.T) {
	e := newEnv(t)
	kept, other := uuid.New(), uuid.New()
	e.store.st.districts[kept] = uuid.New()
	e.store.st.districts[other] = uuid.New()
	ctx, _ := newMember(t, e)
	if _, err := e.svc.UpdateMe(ctx, model.ProfileUpdate{DistrictID: &kept}); err != nil {
		t.Fatal(err)
	}
	e.store.st.inactiveDistricts = map[uuid.UUID]bool{kept: true, other: true}

	tests := []struct {
		name   string
		update model.ProfileUpdate
		want   error
	}{
		{"resend own deactivated district", model.ProfileUpdate{DistrictID: &kept, Lang: ptr(model.LangRU)}, nil},
		{"other change keeps it", model.ProfileUpdate{Lang: ptr(model.LangKK)}, nil},
		{"new choice of a deactivated district", model.ProfileUpdate{DistrictID: &other}, model.ErrUnknownDistrict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.UpdateMe(ctx, tt.update)
			wantErr(t, err, tt.want)
			m, err := e.svc.Me(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if m.DistrictID == nil || *m.DistrictID != kept {
				t.Errorf("district = %v, want the kept %v", m.DistrictID, kept)
			}
		})
	}

	// Another member cannot pick the deactivated district she kept.
	otherCtx, _ := newMember(t, e)
	_, err := e.svc.UpdateMe(otherCtx, model.ProfileUpdate{DistrictID: &kept})
	wantErr(t, err, model.ErrUnknownDistrict)
}

// TestDeactivatedNeedKeptByChild checks that a child card keeps a need the
// sync deactivated, while new cards and new needs only take active codes.
func TestDeactivatedNeedKeptByChild(t *testing.T) {
	e := newEnv(t)
	e.store.st.needs = []string{"autism", "hearing"}
	ctx, _ := newMember(t, e)
	c, err := e.svc.AddChild(ctx, uuid.New(), model.ChildInput{BirthYear: 2019, Needs: []string{"hearing"}})
	if err != nil {
		t.Fatal(err)
	}
	e.store.st.inactiveNeeds = []string{"hearing", "autism"}

	tests := []struct {
		name string
		run  func() error
		want error
	}{
		{"edit keeps the deactivated need", func() error {
			_, err := e.svc.UpdateChild(ctx, c.ID, model.ChildInput{BirthYear: 2018, Needs: []string{"hearing"}})
			return err
		}, nil},
		{"edit cannot add another deactivated need", func() error {
			_, err := e.svc.UpdateChild(ctx, c.ID, model.ChildInput{BirthYear: 2018, Needs: []string{"hearing", "autism"}})
			return err
		}, model.ErrUnknownNeed},
		{"new card cannot take a deactivated need", func() error {
			_, err := e.svc.AddChild(ctx, uuid.New(), model.ChildInput{BirthYear: 2020, Needs: []string{"hearing"}})
			return err
		}, model.ErrUnknownNeed},
		{"unknown child is not found", func() error {
			_, err := e.svc.UpdateChild(ctx, uuid.New(), model.ChildInput{BirthYear: 2018, Needs: []string{}})
			return err
		}, model.ErrChildNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantErr(t, tt.run(), tt.want)
		})
	}
}
