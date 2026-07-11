package fieldauth

import (
	"encoding/json"
	"strings"
	"testing"
)

func parse(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestRedactRemovesNestedFieldAndAddsGovernanceExtension(t *testing.T) {
	body := []byte(`{"data":{"title":{"primaryTitle":"GoT","rating":{"averageRating":9.2,"numVotes":2632043}}}}`)
	out, ok := redactBody(body,
		[][]string{{"title", "rating", "numVotes"}},
		[]string{"Rating.numVotes"}, []string{"public"}, 7)
	if !ok {
		t.Fatal("expected redactable")
	}
	text := string(out)
	if strings.Contains(text, "numVotes") && !strings.Contains(text, "redactedFields") {
		t.Fatalf("value leaked: %s", text)
	}
	doc := parse(t, out)
	rating := doc["data"].(map[string]any)["title"].(map[string]any)["rating"].(map[string]any)
	if _, present := rating["numVotes"]; present {
		t.Fatal("numVotes still present")
	}
	if rating["averageRating"] != 9.2 {
		t.Fatal("sibling field damaged")
	}
	governance := doc["extensions"].(map[string]any)["governance"].(map[string]any)
	if governance["redactedFields"].([]any)[0] != "Rating.numVotes" || governance["revision"] != float64(7) {
		t.Fatalf("bad governance extension: %v", governance)
	}
}

func TestRedactAppliesAcrossListElements(t *testing.T) {
	body := []byte(`{"data":{"title":{"directors":[
		{"primaryName":"A","birthYear":1960},
		{"primaryName":"B","birthYear":1971}]}}}`)
	out, ok := redactBody(body,
		[][]string{{"title", "directors", "birthYear"}},
		[]string{"Name.birthYear"}, nil, 1)
	if !ok {
		t.Fatal("expected redactable")
	}
	doc := parse(t, out)
	directors := doc["data"].(map[string]any)["title"].(map[string]any)["directors"].([]any)
	for i, d := range directors {
		director := d.(map[string]any)
		if _, present := director["birthYear"]; present {
			t.Fatalf("list element %d leaked birthYear: %s", i, out)
		}
		if director["primaryName"] == nil {
			t.Fatalf("list element %d lost sibling data", i)
		}
	}
}

func TestRedactHonorsAliases(t *testing.T) {
	body := []byte(`{"data":{"votes":{"n":{"v":123}}}}`)
	out, ok := redactBody(body, [][]string{{"votes", "n", "v"}}, []string{"Rating.numVotes"}, nil, 1)
	if !ok || strings.Contains(string(out), "123") {
		t.Fatalf("aliased value leaked: %s", out)
	}
}

func TestUnparseableBodyFailsClosed(t *testing.T) {
	if _, ok := redactBody([]byte("<html>nope"), [][]string{{"a"}}, nil, nil, 1); ok {
		t.Fatal("must fail closed on non-JSON")
	}
}

func TestErrorResponseWithNullDataPassesThroughUntouched(t *testing.T) {
	// A subgraph failure (data null) carries no governed data — masking it
	// as a governance denial would disguise every backend error as a
	// permissions problem for role-less callers.
	body := []byte(`{"errors":[{"message":"Failed to fetch from Subgraph 'orchestrator'."}],"data":null}`)
	out, ok := redactBody(body, [][]string{{"hits", "rating", "numVotes"}}, []string{"Rating.numVotes"}, nil, 8)
	if !ok {
		t.Fatal("error responses must pass through, not fail closed")
	}
	if string(out) != string(body) {
		t.Fatalf("error response must be untouched: %s", out)
	}
}
