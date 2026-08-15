package module

import (
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/identity"
)

// notSystemModulePattern rejects IDs starting with $ (system modules).
var notSystemModulePattern = regexp.MustCompile(`^[^$]`)

// infoModel is the key-free view of a module identity shared by the
// iothub_module and iothub_modules data sources.
type infoModel struct {
	ModuleID                   types.String `tfsdk:"module_id"`
	ManagedBy                  types.String `tfsdk:"managed_by"`
	AuthenticationType         types.String `tfsdk:"authentication_type"`
	PrimaryThumbprint          types.String `tfsdk:"primary_thumbprint"`
	SecondaryThumbprint        types.String `tfsdk:"secondary_thumbprint"`
	ETag                       types.String `tfsdk:"etag"`
	GenerationID               types.String `tfsdk:"generation_id"`
	ConnectionState            types.String `tfsdk:"connection_state"`
	ConnectionStateUpdatedTime types.String `tfsdk:"connection_state_updated_time"`
	LastActivityTime           types.String `tfsdk:"last_activity_time"`
	CloudToDeviceMessageCount  types.Int64  `tfsdk:"cloud_to_device_message_count"`
}

var infoAttrTypes = map[string]attr.Type{
	"module_id":                     types.StringType,
	"managed_by":                    types.StringType,
	"authentication_type":           types.StringType,
	"primary_thumbprint":            types.StringType,
	"secondary_thumbprint":          types.StringType,
	"etag":                          types.StringType,
	"generation_id":                 types.StringType,
	"connection_state":              types.StringType,
	"connection_state_updated_time": types.StringType,
	"last_activity_time":            types.StringType,
	"cloud_to_device_message_count": types.Int64Type,
}

// infoAttributes are the computed data-source attributes of infoModel.
func infoAttributes() map[string]schema.Attribute {
	c := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: desc, Computed: true}
	}
	return map[string]schema.Attribute{
		"module_id":                     c("Module ID."),
		"managed_by":                    c("Owner of the module (`managedBy`), e.g. `iotEdge` for system modules."),
		"authentication_type":           c("`sas`, `selfSigned`, `certificateAuthority` or `none`."),
		"primary_thumbprint":            c("Primary X.509 thumbprint (selfSigned)."),
		"secondary_thumbprint":          c("Secondary X.509 thumbprint (selfSigned)."),
		"etag":                          c("Module ETag."),
		"generation_id":                 c("Hub-generated generation ID."),
		"connection_state":              c("`Connected` or `Disconnected` (approximate)."),
		"connection_state_updated_time": c("When the connection state last changed."),
		"last_activity_time":            c("Last activity time."),
		"cloud_to_device_message_count": schema.Int64Attribute{MarkdownDescription: "Queued cloud-to-device messages.", Computed: true},
	}
}

func infoFromHub(m *client.Module) infoModel {
	auth := identity.AuthFromHub(m.Authentication, false)
	return infoModel{
		ModuleID:                   types.StringValue(m.ModuleID),
		ManagedBy:                  identity.StringOrNull(m.ManagedBy),
		AuthenticationType:         auth.Type,
		PrimaryThumbprint:          auth.PrimaryThumbprint,
		SecondaryThumbprint:        auth.SecondaryThumbprint,
		ETag:                       types.StringValue(m.ETag),
		GenerationID:               types.StringValue(m.GenerationID),
		ConnectionState:            identity.StringOrNull(m.ConnectionState),
		ConnectionStateUpdatedTime: identity.StringOrNull(m.ConnectionStateUpdatedTime),
		LastActivityTime:           identity.StringOrNull(m.LastActivityTime),
		CloudToDeviceMessageCount:  types.Int64Value(m.CloudToDeviceMessageCount),
	}
}

func (i infoModel) object() types.Object {
	return types.ObjectValueMust(infoAttrTypes, map[string]attr.Value{
		"module_id":                     i.ModuleID,
		"managed_by":                    i.ManagedBy,
		"authentication_type":           i.AuthenticationType,
		"primary_thumbprint":            i.PrimaryThumbprint,
		"secondary_thumbprint":          i.SecondaryThumbprint,
		"etag":                          i.ETag,
		"generation_id":                 i.GenerationID,
		"connection_state":              i.ConnectionState,
		"connection_state_updated_time": i.ConnectionStateUpdatedTime,
		"last_activity_time":            i.LastActivityTime,
		"cloud_to_device_message_count": i.CloudToDeviceMessageCount,
	})
}
