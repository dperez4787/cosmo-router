package fieldauth

import (
	"reflect"
	"strings"
	"testing"
)

// Shaped like the composed client schema: entities, cross-subgraph fields,
// and the orchestrator's union.
const testSchema = `
schema { query: Query }

type Query {
  title(tconst: ID!): Title
  name(nconst: ID!): Name
  search(q: String!): [SearchResult!]!
}

type Title {
  tconst: ID!
  primaryTitle: String
  rating: Rating
  directors: [Name!]
}

type Rating {
  averageRating: Float!
  numVotes: Int!
}

type Name {
  nconst: ID!
  primaryName: String
  birthYear: Int
  deathYear: Int
}

union SearchResult = Title | Name
`

func selections(t *testing.T, operation string) []Selection {
	t.Helper()
	schema, err := parseSchema(testSchema)
	if err != nil {
		t.Fatal(err)
	}
	out, err := extractSelections(operation, schema)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func coordinates(t *testing.T, operation string) []string {
	t.Helper()
	return uniqueCoordinates(selections(t, operation))
}

func pathOf(t *testing.T, sels []Selection, coordinate string) string {
	t.Helper()
	for _, s := range sels {
		if s.Coordinate == coordinate {
			return strings.Join(s.Path, ".")
		}
	}
	t.Fatalf("coordinate %q not found in %v", coordinate, sels)
	return ""
}

func TestSimpleSelection(t *testing.T) {
	got := coordinates(t, `{ title(tconst: "tt1") { primaryTitle rating { numVotes } } }`)
	want := []string{"Query.title", "Rating.numVotes", "Title.primaryTitle", "Title.rating"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAliasesResolveCoordinatesButAliasThePath(t *testing.T) {
	sels := selections(t, `{ votes: title(tconst: "tt1") { n: rating { v: numVotes } } }`)
	got := uniqueCoordinates(sels)
	want := []string{"Query.title", "Rating.numVotes", "Title.rating"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("aliases must not hide coordinates: got %v want %v", got, want)
	}
	// Redaction must target the RESPONSE keys, i.e. the aliases.
	if p := pathOf(t, sels, "Rating.numVotes"); p != "votes.n.v" {
		t.Fatalf("path must use aliases: got %q", p)
	}
}

func TestNamedFragmentsAndTypeConditions(t *testing.T) {
	sels := selections(t, `
		query Q { title(tconst: "tt1") { ...ratingBits directors { ...person } } }
		fragment ratingBits on Title { rating { averageRating } }
		fragment person on Name { primaryName birthYear }
	`)
	got := uniqueCoordinates(sels)
	want := []string{
		"Name.birthYear", "Name.primaryName",
		"Query.title", "Rating.averageRating", "Title.directors", "Title.rating",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// Fragment names must not appear in response paths.
	if p := pathOf(t, sels, "Name.birthYear"); p != "title.directors.birthYear" {
		t.Fatalf("fragment polluted the path: %q", p)
	}
}

func TestUnionInlineFragments(t *testing.T) {
	sels := selections(t, `
		{ search(q: "x") { __typename ... on Title { primaryTitle } ... on Name { deathYear } } }
	`)
	got := uniqueCoordinates(sels)
	want := []string{"Name.deathYear", "Query.search", "Title.primaryTitle"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// Inline fragment type conditions are not response keys.
	if p := pathOf(t, sels, "Name.deathYear"); p != "search.deathYear" {
		t.Fatalf("type condition polluted the path: %q", p)
	}
}

func TestIntrospectionMetaFieldsAreSkipped(t *testing.T) {
	got := coordinates(t, `{ __typename __schema { types { name } } title(tconst: "t") { tconst } }`)
	for _, coordinate := range got {
		switch coordinate {
		case "Query.title", "Title.tconst":
		default:
			t.Fatalf("unexpected coordinate from introspection: %q (all: %v)", coordinate, got)
		}
	}
}
