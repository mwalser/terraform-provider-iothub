package identity

import (
	"github.com/hashicorp/terraform-plugin-framework/attr"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
)

// AuthInfoAttrTypes is the object type of the read-only `authentication`
// attribute of the identity data sources: the resource's shape without keys.
var AuthInfoAttrTypes = map[string]attr.Type{
	"type":                 types.StringType,
	"primary_thumbprint":   types.StringType,
	"secondary_thumbprint": types.StringType,
}

// AuthInfoAttribute is the `authentication` attribute of the identity data
// sources; subject ("device", "module") is used in descriptions.
func AuthInfoAttribute(subject string) dsschema.SingleNestedAttribute {
	c := func(desc string) dsschema.StringAttribute {
		return dsschema.StringAttribute{MarkdownDescription: desc, Computed: true}
	}
	return dsschema.SingleNestedAttribute{
		MarkdownDescription: "How the " + subject + " authenticates. Keys are not exposed here.",
		Computed:            true,
		Attributes: map[string]dsschema.Attribute{
			"type":                 c("`sas`, `selfSigned`, `certificateAuthority`, or `none` for identities without credentials such as the hub's system modules."),
			"primary_thumbprint":   c("Primary X.509 thumbprint, for `selfSigned` authentication."),
			"secondary_thumbprint": c("Secondary X.509 thumbprint, for `selfSigned` authentication."),
		},
	}
}

// AuthInfoFromHub renders a hub authentication mechanism as the data-source
// object.
func AuthInfoFromHub(am *client.AuthenticationMechanism) types.Object {
	a := AuthFromHub(am, false)
	return types.ObjectValueMust(AuthInfoAttrTypes, map[string]attr.Value{
		"type":                 a.Type,
		"primary_thumbprint":   a.PrimaryThumbprint,
		"secondary_thumbprint": a.SecondaryThumbprint,
	})
}
