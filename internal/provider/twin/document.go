// Package twin implements iothub_device_twin and iothub_module_twin
// (resources and data sources): partial, leaf-path ownership of twin tags
// and desired properties as specified in CONCEPT.md §6.3, on top of the
// twinpatch engine.
package twin

import (
	"github.com/mwalser/terraform-provider-iothub/internal/provider/jsondoc"
	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

// DocumentType is the attribute type of twin sections (`tags`,
// `desired_properties`): a JSON object compared semantically and validated
// against the service's twin rules (twinpatch.Validate).
var DocumentType = jsondoc.Type{Name: "twin", Validate: func(doc map[string]any) []string {
	var out []string
	for _, p := range twinpatch.Validate(doc) {
		out = append(out, p.String())
	}
	return out
}}

// Document is a DocumentType value.
type Document = jsondoc.Value

// NewDocumentValue wraps a JSON string.
func NewDocumentValue(s string) Document { return jsondoc.NewValue(DocumentType, s) }

// NewDocumentNull returns a null document.
func NewDocumentNull() Document { return jsondoc.NewNull(DocumentType) }
