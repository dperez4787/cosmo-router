package fieldauth

import (
	"encoding/json"
)

// redactBody removes the given response paths from the GraphQL response's
// data tree and records what happened in extensions.governance — an absent
// key, never an errors entry, so naive clients (and graphql-request, which
// throws on any errors array) keep working with partial data.
//
// Paths are alias-aware response keys relative to the data root. Arrays are
// transparent: a path applies to every element, so entity lists redact
// uniformly. Returns ok=false when the body isn't a JSON object with a data
// map — the caller must then fail closed rather than forward unredacted.
func redactBody(body []byte, paths [][]string, redacted []string, roles []string, revision int64) ([]byte, bool) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, false
	}
	data, ok := doc["data"].(map[string]any)
	if !ok {
		// data is null/absent (an execution error response): it carries no
		// governed data, so forward it untouched — masking a subgraph error
		// as a governance denial misleads every role-less caller.
		return body, true
	}
	for _, path := range paths {
		removePath(data, path)
	}

	extensions, _ := doc["extensions"].(map[string]any)
	if extensions == nil {
		extensions = map[string]any{}
		doc["extensions"] = extensions
	}
	if roles == nil {
		roles = []string{}
	}
	extensions["governance"] = map[string]any{
		"redactedFields": redacted,
		"roles":          roles,
		"revision":       revision,
	}

	out, err := json.Marshal(doc)
	if err != nil {
		return nil, false
	}
	return out, true
}

func removePath(node any, path []string) {
	if len(path) == 0 {
		return
	}
	switch n := node.(type) {
	case map[string]any:
		if len(path) == 1 {
			delete(n, path[0])
			return
		}
		if child, ok := n[path[0]]; ok {
			removePath(child, path[1:])
		}
	case []any:
		// Lists are transparent: apply the same path to every element.
		for _, item := range n {
			removePath(item, path)
		}
	}
}
