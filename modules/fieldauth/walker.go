package fieldauth

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/wundergraph/graphql-go-tools/v2/pkg/ast"
	"github.com/wundergraph/graphql-go-tools/v2/pkg/astparser"
	"github.com/wundergraph/graphql-go-tools/v2/pkg/asttransform"
	"github.com/wundergraph/graphql-go-tools/v2/pkg/astvisitor"
)

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

// extractCoordinates returns the unique Type.field schema coordinates the
// operation selects, resolved via a typed walk of the (normalized) operation
// against the client schema — fragments and inline spreads resolve to their
// type conditions. Introspection meta fields (__typename, __schema, __type)
// are skipped: they reveal shape, not data.
func extractCoordinates(operation string, schema *ast.Document) ([]string, error) {
	opDoc, report := astparser.ParseGraphqlDocumentString(operation)
	if report.HasErrors() {
		return nil, fmt.Errorf("parsing operation: %s", report.Error())
	}

	walker := astvisitor.NewWalker(48)
	visitor := &coordinateVisitor{
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

	coordinates := make([]string, 0, len(visitor.seen))
	for coordinate := range visitor.seen {
		coordinates = append(coordinates, coordinate)
	}
	sort.Strings(coordinates)
	return coordinates, nil
}

type coordinateVisitor struct {
	walker    *astvisitor.Walker
	operation *ast.Document
	schema    *ast.Document
	seen      map[string]bool
}

func (v *coordinateVisitor) EnterField(ref int) {
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
	v.seen[typeName+"."+fieldName] = true
}
