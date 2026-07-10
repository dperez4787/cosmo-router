package fieldauth

import (
	"reflect"
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

func coordinates(t *testing.T, operation string) []string {
	t.Helper()
	schema, err := parseSchema(testSchema)
	if err != nil {
		t.Fatal(err)
	}
	coords, err := extractCoordinates(operation, schema)
	if err != nil {
		t.Fatal(err)
	}
	return coords
}

func TestSimpleSelection(t *testing.T) {
	got := coordinates(t, `{ title(tconst: "tt1") { primaryTitle rating { numVotes } } }`)
	want := []string{"Query.title", "Rating.numVotes", "Title.primaryTitle", "Title.rating"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAliasesResolveToRealFieldNames(t *testing.T) {
	got := coordinates(t, `{ votes: title(tconst: "tt1") { n: rating { v: numVotes } } }`)
	want := []string{"Query.title", "Rating.numVotes", "Title.rating"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("aliases must not hide coordinates: got %v want %v", got, want)
	}
}

func TestNamedFragmentsAndTypeConditions(t *testing.T) {
	got := coordinates(t, `
		query Q { title(tconst: "tt1") { ...ratingBits directors { ...person } } }
		fragment ratingBits on Title { rating { averageRating } }
		fragment person on Name { primaryName birthYear }
	`)
	want := []string{
		"Name.birthYear", "Name.primaryName",
		"Query.title", "Rating.averageRating", "Title.directors", "Title.rating",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestUnionInlineFragments(t *testing.T) {
	got := coordinates(t, `
		{ search(q: "x") { __typename ... on Title { primaryTitle } ... on Name { deathYear } } }
	`)
	want := []string{"Name.deathYear", "Query.search", "Title.primaryTitle"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
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
