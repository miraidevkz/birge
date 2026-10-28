package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"birge/internal/domains/emergency/api"
	"birge/internal/domains/emergency/model"
	"birge/internal/platform/auth"
	"birge/internal/platform/staffauth"
)

// contactPair makes friend a confirmed member contact of owner and returns
// the contact id.
func (hs *harness) contactPair(t *testing.T, owner, friend *auth.Principal, accept bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	hs.members.Meet(owner.MemberID, friend.MemberID)
	c, err := hs.svc.AddTrustedContact(ctx, *owner, uuid.Must(uuid.NewV7()), model.NewTrustedContact{Kind: model.ContactMember, MemberID: friend.MemberID})
	if err != nil {
		t.Fatal(err)
	}
	if accept {
		if err := hs.svc.AcceptTrustedInvitation(ctx, *friend, c.ID); err != nil {
			t.Fatal(err)
		}
	}
	return c.ID
}

func (hs *harness) raise(t *testing.T, p *auth.Principal, typ string) uuid.UUID {
	t.Helper()
	v, err := hs.svc.RaiseSos(context.Background(), *p, uuid.Must(uuid.NewV7()), typ, "example sos text")
	if err != nil {
		t.Fatal(err)
	}
	return v.ID
}

func TestTrustedSosOverHTTP(t *testing.T) {
	hs := newHarness()
	owner := hs.member(false, nil)
	friend := hs.member(false, nil)
	invited := hs.member(false, nil)
	stranger := hs.member(false, nil)
	hs.contactPair(t, owner, friend, true)
	hs.contactPair(t, owner, invited, false)
	id := hs.raise(t, owner, model.SosChildcare)
	path := "/trusted/sos/" + id.String()

	for name, tc := range map[string]struct {
		p    *auth.Principal
		path string
		want int
	}{
		"anonymous": {nil, path, http.StatusUnauthorized},
		"stranger":  {stranger, path, http.StatusForbidden},
		"invited":   {invited, path, http.StatusForbidden},
		"owner":     {owner, path, http.StatusForbidden},
		"unknown":   {friend, "/trusted/sos/" + uuid.NewString(), http.StatusNotFound},
	} {
		if rec := hs.do(t, tc.p, http.MethodGet, tc.path, ""); rec.Code != tc.want {
			t.Errorf("%s GET: %d %s, want %d", name, rec.Code, rec.Body, tc.want)
		}
		if rec := hs.do(t, tc.p, http.MethodPost, tc.path+"/respond", ""); rec.Code != tc.want {
			t.Errorf("%s respond: %d %s, want %d", name, rec.Code, rec.Body, tc.want)
		}
	}

	rec := hs.do(t, friend, http.MethodGet, path, "")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("friend GET: %d %q %s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body)
	}
	got := decode[api.TrustedSos](t, rec)
	if got.Type != api.SosType(model.SosChildcare) || got.Text != "example sos text" || got.RespondedByMe || got.Status != api.TrustedSosOpen {
		t.Fatalf("trusted sos = %+v", got)
	}
	for range 2 {
		if rec := hs.do(t, friend, http.MethodPost, path+"/respond", ""); rec.Code != http.StatusNoContent {
			t.Fatalf("respond: %d %s", rec.Code, rec.Body)
		}
	}
	if got := decode[api.TrustedSos](t, hs.do(t, friend, http.MethodGet, path, "")); !got.RespondedByMe {
		t.Fatal("respondedByMe after responding")
	}

	closed := hs.raise(t, owner, model.SosOther)
	if _, err := hs.svc.CloseSos(context.Background(), *owner, closed); err != nil {
		t.Fatal(err)
	}
	if rec := hs.do(t, friend, http.MethodPost, "/trusted/sos/"+closed.String()+"/respond", ""); rec.Code != http.StatusConflict {
		t.Fatalf("respond on closed: %d %s", rec.Code, rec.Body)
	}
}

func TestTrustedInvitationOverHTTP(t *testing.T) {
	hs := newHarness()
	owner := hs.member(false, nil)
	invited := hs.member(false, nil)
	other := hs.member(false, nil)
	cid := hs.contactPair(t, owner, invited, false)
	path := "/me/trusted-invitations/" + cid.String()

	rec := hs.do(t, invited, http.MethodGet, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body)
	}
	if inv := decode[api.TrustedInvitation](t, rec); inv.ContactId != cid || inv.Status != api.TrustedInvitationInvited || inv.OwnerDisplayName != "Example" {
		t.Fatalf("invitation = %+v", inv)
	}
	for _, p := range []*auth.Principal{nil, other, owner} {
		want := http.StatusNotFound
		if p == nil {
			want = http.StatusUnauthorized
		}
		if rec := hs.do(t, p, http.MethodGet, path, ""); rec.Code != want {
			t.Errorf("GET by %v: %d, want %d", p, rec.Code, want)
		}
		if rec := hs.do(t, p, http.MethodPost, path+"/decline", ""); rec.Code != want {
			t.Errorf("decline by %v: %d, want %d", p, rec.Code, want)
		}
	}
	for range 2 {
		if rec := hs.do(t, invited, http.MethodPost, path+"/decline", ""); rec.Code != http.StatusNoContent {
			t.Fatalf("decline: %d %s", rec.Code, rec.Body)
		}
	}
	if rec := hs.do(t, invited, http.MethodGet, path, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET after decline: %d", rec.Code)
	}
}

func TestStaffSosOverHTTP(t *testing.T) {
	hs := newHarness()
	d := uuid.New()
	author := hs.member(true, &d)
	id := hs.raise(t, author, model.SosChildcare)
	mod := &auth.Principal{IdentityID: uuid.New(), Staff: true, MFA: true, Roles: []string{staffauth.RoleModerator}}
	noMFA := &auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{staffauth.RoleModerator}}
	psych := &auth.Principal{IdentityID: uuid.New(), Staff: true, MFA: true, Roles: []string{staffauth.RolePsychologist}}

	routes := []struct{ method, path string }{
		{http.MethodGet, "/staff/sos"},
		{http.MethodPost, "/staff/sos/" + id.String() + "/seen"},
		{http.MethodPost, "/staff/sos/" + id.String() + "/close"},
	}
	for _, r := range routes {
		for name, tc := range map[string]struct {
			p    *auth.Principal
			want int
		}{
			"anonymous":    {nil, http.StatusUnauthorized},
			"member":       {author, http.StatusForbidden},
			"no MFA":       {noMFA, http.StatusForbidden},
			"psychologist": {psych, http.StatusForbidden},
		} {
			if rec := hs.do(t, tc.p, r.method, r.path, ""); rec.Code != tc.want {
				t.Errorf("%s %s %s: %d, want %d", name, r.method, r.path, rec.Code, tc.want)
			}
		}
	}

	rec := hs.do(t, mod, http.MethodGet, "/staff/sos", "")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("list: %d %q %s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body)
	}
	page := decode[api.StaffSosPage](t, rec)
	if len(page.Items) != 1 || page.Items[0].Id != id || page.Items[0].DisplayName != "Example" ||
		page.Items[0].DistrictId == nil || *page.Items[0].DistrictId != d || page.NextCursor != nil {
		t.Fatalf("page = %+v", page)
	}
	if strings.Contains(rec.Body.String(), "phone") {
		t.Fatalf("staff list mentions a phone: %s", rec.Body)
	}
	if rec := hs.do(t, mod, http.MethodGet, "/staff/sos?status=closed", ""); rec.Code != http.StatusOK ||
		len(decode[api.StaffSosPage](t, rec).Items) != 0 {
		t.Fatalf("closed list: %d %s", rec.Code, rec.Body)
	}
	if rec := hs.do(t, mod, http.MethodGet, "/staff/sos?cursor=bad!", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor: %d %s", rec.Code, rec.Body)
	}

	rec = hs.do(t, mod, http.MethodPost, "/staff/sos/"+id.String()+"/seen", "")
	if s := decode[api.StaffSos](t, rec); rec.Code != http.StatusOK || s.SeenBy == nil || *s.SeenBy != mod.IdentityID {
		t.Fatalf("seen: %d %s", rec.Code, rec.Body)
	}
	rec = hs.do(t, mod, http.MethodPost, "/staff/sos/"+id.String()+"/close", "")
	if s := decode[api.StaffSos](t, rec); rec.Code != http.StatusOK || s.Status != api.StaffSosStatusClosed || s.ClosedAt == nil {
		t.Fatalf("close: %d %s", rec.Code, rec.Body)
	}
	if rec := hs.do(t, mod, http.MethodPost, "/staff/sos/"+uuid.NewString()+"/seen", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("seen unknown: %d", rec.Code)
	}
}
