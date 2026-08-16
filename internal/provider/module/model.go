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
	Authentication             types.Object `tfsdk:"authentication"`
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
	"authentication":                types.ObjectType{AttrTypes: identity.AuthInfoAttrTypes},
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
		"managed_by":                    c("Owner of the module, for example `iotEdge` for system modules."),
		"authentication":                identity.AuthInfoAttribute("module"),
		"etag":                          c("ETag of the module identity."),
		"generation_id":                 c("Hub-generated ID that changes when a module with the same ID is re-created."),
		"connection_state":              c("`Connected` or `Disconnected`. Approximate."),
		"connection_state_updated_time": c("When the connection state last changed."),
		"last_activity_time":            c("Last time the module connected, sent or received a message."),
		"cloud_to_device_message_count": schema.Int64Attribute{MarkdownDescription: "Queued cloud-to-device messages.", Computed: true},
	}
}

func infoFromHub(m *client.Module) infoModel {
	return infoModel{
		ModuleID:                   types.StringValue(m.ModuleID),
		ManagedBy:                  identity.StringOrNull(m.ManagedBy),
		Authentication:             identity.AuthInfoFromHub(m.Authentication),
		ETag:                       types.StringValue(m.ETag),
		GenerationID:               types.StringValue(m.GenerationID),
		ConnectionState:            identity.StringOrNull(m.ConnectionState),
		ConnectionStateUpdatedTime: identity.TimeOrNull(m.ConnectionStateUpdatedTime),
		LastActivityTime:           identity.TimeOrNull(m.LastActivityTime),
		CloudToDeviceMessageCount:  types.Int64Value(m.CloudToDeviceMessageCount),
	}
}

func (i infoModel) object() types.Object {
	return types.ObjectValueMust(infoAttrTypes, map[string]attr.Value{
		"module_id":                     i.ModuleID,
		"managed_by":                    i.ManagedBy,
		"authentication":                i.Authentication,
		"etag":                          i.ETag,
		"generation_id":                 i.GenerationID,
		"connection_state":              i.ConnectionState,
		"connection_state_updated_time": i.ConnectionStateUpdatedTime,
		"last_activity_time":            i.LastActivityTime,
		"cloud_to_device_message_count": i.CloudToDeviceMessageCount,
	})
}
