package fieldauth

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/wundergraph/graphql-go-tools/v2/pkg/ast"
	"github.com/wundergraph/graphql-go-tools/v2/pkg/astnormalization"
	"github.com/wundergraph/graphql-go-tools/v2/pkg/astparser"
	"github.com/wundergraph/graphql-go-tools/v2/pkg/asttransform"
	"github.com/wundergraph/graphql-go-tools/v2/pkg/astvisitor"
)

// Selection is one selected field: its schema coordinate (Type.field) for
// policy decisions, and its response path (alias-aware keys from the data
// root) for redaction. The same coordinate can appear under many paths.
type Selection struct {
	Coordinate string
	Path       []string
}

// loadClientSchema pulls the composed client schema out of the same
// execution config the router executes (engineConfig.graphqlSchema), so the
// walker always resolves coordinates against exactly the schema being served.
func loadClientSchema(path string) (*ast.Document, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading execution config: %w", err)
	}
	var config struct {
		EngineConfig struct {
			GraphqlSchema string `json:"graphqlSchema"`
		} `json:"engineConfig"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("parsing execution config: %w", err)
	}
	if config.EngineConfig.GraphqlSchema == "" {
		return nil, fmt.Errorf("execution config has no engineConfig.graphqlSchema")
	}
	return parseSchema(config.EngineConfig.GraphqlSchema)
}

func parseSchema(sdl string) (*ast.Document, error) {
	doc, report := astparser.ParseGraphqlDocumentString(sdl)
	if report.HasErrors() {
		return nil, fmt.Errorf("parsing client schema: %s", report.Error())
	}
	// Adds the base scalars/introspection types the SDL omits; without this
	// the typed walk cannot resolve every field.
	if err := asttransform.MergeDefinitionWithBaseSchema(&doc); err != nil {
		return nil, fmt.Errorf("merging base schema: %w", err)
	}
	return &doc, nil
}

// extractSelections resolves the (normalized) operation's fields via a typed
// walk against the client schema. Fragments and inline spreads resolve to
// their type conditions; response paths are built from field ancestors only,
// so fragment path segments never pollute them. Introspection meta fields
// (__typename, __schema, ...) are skipped: they reveal shape, not data.
func extractSelections(operation string, schema *ast.Document) ([]Selection, error) {
	opDoc, report := astparser.ParseGraphqlDocumentString(operation)
	if report.HasErrors() {
		return nil, fmt.Errorf("parsing operation: %s", report.Error())
	}
	// Inline named fragments before walking: response paths are built from
	// field ancestors, and only an inlined operation carries the spread
	// site's fields as ancestors. (Also makes us independent of how much
	// normalization the router applied to Content().)
	astnormalization.NewNormalizer(true, false).NormalizeOperation(&opDoc, schema, &report)
	if report.HasErrors() {
		return nil, fmt.Errorf("normalizing operation: %s", report.Error())
	}

	walker := astvisitor.NewWalker(48)
	visitor := &selectionVisitor{
		walker:    &walker,
		operation: &opDoc,
		schema:    schema,
		seen:      map[string]bool{},
	}
	walker.RegisterEnterFieldVisitor(visitor)
	walker.Walk(&opDoc, schema, &report)
	if report.HasErrors() {
		return nil, fmt.Errorf("walking operation: %s", report.Error())
	}
	return visitor.selections, nil
}

func uniqueCoordinates(selections []Selection) []string {
	set := map[string]bool{}
	for _, s := range selections {
		set[s.Coordinate] = true
	}
	out := make([]string, 0, len(set))
	for coordinate := range set {
		out = append(out, coordinate)
	}
	sort.Strings(out)
	return out
}

type selectionVisitor struct {
	walker     *astvisitor.Walker
	operation  *ast.Document
	schema     *ast.Document
	seen       map[string]bool
	selections []Selection
}

func (v *selectionVisitor) EnterField(ref int) {
	fieldName := v.operation.FieldNameString(ref)
	if strings.HasPrefix(fieldName, "__") {
		return
	}
	enclosing := v.walker.EnclosingTypeDefinition
	typeName := v.schema.NodeNameString(enclosing)
	// Selections inside introspection types (__Schema, __Type, ...) reveal
	// schema shape, not data — never governed.
	if typeName == "" || strings.HasPrefix(typeName, "__") {
		return
	}

	// Response path = alias-or-name of every FIELD ancestor + this field.
	// Non-field ancestors (operation root, fragments, type conditions) are
	// not response keys and are skipped.
	var path []string
	for _, ancestor := range v.walker.Ancestors {
		if ancestor.Kind == ast.NodeKindField {
			path = append(path, v.operation.FieldAliasOrNameString(ancestor.Ref))
		}
	}
	path = append(path, v.operation.FieldAliasOrNameString(ref))

	coordinate := typeName + "." + fieldName
	key := coordinate + "|" + strings.Join(path, ".")
	if v.seen[key] {
		return
	}
	v.seen[key] = true
	v.selections = append(v.selections, Selection{Coordinate: coordinate, Path: path})
}
