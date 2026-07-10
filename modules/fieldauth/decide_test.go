package fieldauth

import (
	"reflect"
	"testing"
)

func testBundle() *Bundle {
	return &Bundle{
		Revision:       7,
		DefaultPosture: "allow-unless-governed",
		Fields: map[string]FieldEntry{
			"Rating.numVotes": {AllowedRoles: []string{"analyst"}, Subgraph: "ratings"},
			"Name.birthYear":  {AllowedRoles: []string{"analyst"}, Subgraph: "names"},
			"Name.deathYear":  {AllowedRoles: []string{}, Subgraph: "names"},
		},
		Principals: map[string][]string{
			"analyst@example.com": {"analyst", "public"},
		},
	}
}

func TestRolesClaimWins(t *testing.T) {
	roles := resolveRoles(map[string]any{
		"roles": []any{"analyst", "public"},
		"email": "someone@else.com",
	}, testBundle().Principals)
	if !reflect.DeepEqual(roles, []string{"analyst", "public"}) {
		t.Fatalf("got %v", roles)
	}
}

func TestPrincipalsFallbackByEmail(t *testing.T) {
	roles := resolveRoles(map[string]any{"email": "analyst@example.com"}, testBundle().Principals)
	if !reflect.DeepEqual(roles, []string{"analyst", "public"}) {
		t.Fatalf("got %v", roles)
	}
}

func TestUnknownIdentityHasNoRoles(t *testing.T) {
	if roles := resolveRoles(map[string]any{"email": "stranger@example.com", "sub": "123"},
		testBundle().Principals); roles != nil {
		t.Fatalf("got %v", roles)
	}
	if roles := resolveRoles(nil, testBundle().Principals); roles != nil {
		t.Fatalf("nil claims: got %v", roles)
	}
}

func TestUngovernedFieldsAlwaysPass(t *testing.T) {
	denied := deniedCoordinates(
		[]string{"Query.title", "Title.primaryTitle", "Rating.averageRating"},
		testBundle(), nil)
	if denied != nil {
		t.Fatalf("ungoverned coordinates denied: %v", denied)
	}
}

func TestGovernedFieldRequiresRole(t *testing.T) {
	coords := []string{"Rating.numVotes", "Title.primaryTitle"}

	if denied := deniedCoordinates(coords, testBundle(), []string{"analyst"}); denied != nil {
		t.Fatalf("analyst should pass: %v", denied)
	}
	denied := deniedCoordinates(coords, testBundle(), []string{"public"})
	if !reflect.DeepEqual(denied, []string{"Rating.numVotes"}) {
		t.Fatalf("public should be denied numVotes only: %v", denied)
	}
}

func TestEmptyAllowedRolesDeniesEveryone(t *testing.T) {
	denied := deniedCoordinates([]string{"Name.deathYear"}, testBundle(), []string{"analyst", "public"})
	if !reflect.DeepEqual(denied, []string{"Name.deathYear"}) {
		t.Fatalf("got %v", denied)
	}
}
