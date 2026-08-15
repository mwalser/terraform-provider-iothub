// Package provider implements the Terraform provider for the Azure IoT Hub
// data plane. Design and decisions: CONCEPT.md at the repository root.
package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/list"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/actions"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/configuration"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/device"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/jobs"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/module"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/query"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/statistics"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/twin"
)

// Ensure IoTHubProvider satisfies the provider interfaces it implements.
var (
	_ provider.Provider                       = &IoTHubProvider{}
	_ provider.ProviderWithEphemeralResources = &IoTHubProvider{}
	_ provider.ProviderWithActions            = &IoTHubProvider{}
	_ provider.ProviderWithListResources      = &IoTHubProvider{}
)

// IoTHubProvider is the provider implementation.
type IoTHubProvider struct {
	// version is the provider version on release, "dev" for local builds and
	// "test" under acceptance tests.
	version string
}

// providerModel maps the provider schema.
type providerModel struct {
	Hostname                  types.String `tfsdk:"hostname"`
	TenantID                  types.String `tfsdk:"tenant_id"`
	ClientID                  types.String `tfsdk:"client_id"`
	ClientSecret              types.String `tfsdk:"client_secret"`
	ClientCertificatePath     types.String `tfsdk:"client_certificate_path"`
	ClientCertificatePassword types.String `tfsdk:"client_certificate_password"`
	UseOIDC                   types.Bool   `tfsdk:"use_oidc"`
	OIDCTokenFilePath         types.String `tfsdk:"oidc_token_file_path"`
	UseMSI                    types.Bool   `tfsdk:"use_msi"`
	UseCLI                    types.Bool   `tfsdk:"use_cli"`
	ConnectionString          types.String `tfsdk:"connection_string"`
}

func (p *IoTHubProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "iothub"
	resp.Version = p.version
}

func (p *IoTHubProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the Azure IoT Hub **data plane**: device and module identities, twins, " +
			"automatic device management configurations, IoT Edge deployments, jobs and direct methods. " +
			"The hub itself, and everything else under Azure Resource Manager, is managed with the `azurerm` provider.\n\n" +
			"Authentication is Microsoft Entra ID by default. Setting `connection_string` switches to SAS authentication with a " +
			"hub shared access policy. Throttled requests are retried automatically until the operation's timeout. Requires " +
			"Terraform 1.14 or later.",
		Attributes: map[string]schema.Attribute{
			"hostname": schema.StringAttribute{
				MarkdownDescription: "Default IoT Hub hostname, for example `contoso.azure-devices.net`. Every resource, data source, " +
					"ephemeral resource, action and list resource can override it with its own `hostname`. Falls back to " +
					"`IOTHUB_HOSTNAME`. Derived from `connection_string` when that is set.",
				Optional: true,
			},
			"tenant_id": schema.StringAttribute{
				MarkdownDescription: "Entra ID tenant. Falls back to `ARM_TENANT_ID` or `AZURE_TENANT_ID`.",
				Optional:            true,
			},
			"client_id": schema.StringAttribute{
				MarkdownDescription: "Entra ID application (client) ID. Falls back to `ARM_CLIENT_ID` or `AZURE_CLIENT_ID`.",
				Optional:            true,
			},
			"client_secret": schema.StringAttribute{
				MarkdownDescription: "Client secret for service-principal authentication. Falls back to `ARM_CLIENT_SECRET` or `AZURE_CLIENT_SECRET`.",
				Optional:            true,
				Sensitive:           true,
			},
			"client_certificate_path": schema.StringAttribute{
				MarkdownDescription: "Path to a PEM or PKCS#12 client certificate for service-principal authentication. Falls back to `ARM_CLIENT_CERTIFICATE_PATH` or `AZURE_CLIENT_CERTIFICATE_PATH`.",
				Optional:            true,
			},
			"client_certificate_password": schema.StringAttribute{
				MarkdownDescription: "Password of the client certificate, if any. Falls back to `ARM_CLIENT_CERTIFICATE_PASSWORD` or `AZURE_CLIENT_CERTIFICATE_PASSWORD`.",
				Optional:            true,
				Sensitive:           true,
			},
			"use_oidc": schema.BoolAttribute{
				MarkdownDescription: "Authenticate with a federated workload identity token, as used by GitHub Actions, HCP Terraform or Kubernetes. Falls back to `ARM_USE_OIDC`.",
				Optional:            true,
			},
			"oidc_token_file_path": schema.StringAttribute{
				MarkdownDescription: "File containing the federated token when `use_oidc` is set. Falls back to `ARM_OIDC_TOKEN_FILE_PATH` or `AZURE_FEDERATED_TOKEN_FILE`.",
				Optional:            true,
			},
			"use_msi": schema.BoolAttribute{
				MarkdownDescription: "Authenticate with the managed identity of the machine running Terraform. Falls back to `ARM_USE_MSI`.",
				Optional:            true,
			},
			"use_cli": schema.BoolAttribute{
				MarkdownDescription: "Authenticate with the Azure CLI login. Falls back to `ARM_USE_CLI`.",
				Optional:            true,
			},
			"connection_string": schema.StringAttribute{
				MarkdownDescription: "Connection string of a hub shared access policy (`HostName=…;SharedAccessKeyName=…;SharedAccessKey=…`). " +
					"Setting it selects SAS authentication instead of Entra ID. Falls back to `IOTHUB_CONNECTION_STRING`. " +
					"`azurerm_iothub_shared_access_policy` exposes it as `primary_connection_string`.",
				Optional:  true,
				Sensitive: true,
			},
		},
	}
}

func (p *IoTHubProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Values may be unknown during plan when they reference resources that
	// do not exist yet (typically hostname = azurerm_iothub.x.hostname).
	// Resources tolerate an unknown provider-level hostname as long as they
	// set their own; everything else must be known to authenticate.
	mustBeKnown := []struct {
		name string
		v    interface{ IsUnknown() bool }
	}{
		{"tenant_id", data.TenantID}, {"client_id", data.ClientID}, {"client_secret", data.ClientSecret},
		{"client_certificate_path", data.ClientCertificatePath}, {"client_certificate_password", data.ClientCertificatePassword},
		{"use_oidc", data.UseOIDC}, {"oidc_token_file_path", data.OIDCTokenFilePath}, {"use_msi", data.UseMSI},
		{"use_cli", data.UseCLI}, {"connection_string", data.ConnectionString},
	}
	for _, a := range mustBeKnown {
		if a.v.IsUnknown() {
			resp.Diagnostics.AddAttributeError(pathRoot(a.name), "Unknown provider configuration value",
				"The provider cannot authenticate while \""+a.name+"\" is unknown. Give it a value that is known at plan "+
					"time (a literal, variable or output of an already-applied resource); only \"hostname\" may be unknown.")
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	raw := rawConfig{
		TenantID:                  data.TenantID.ValueString(),
		ClientID:                  data.ClientID.ValueString(),
		ClientSecret:              data.ClientSecret.ValueString(),
		ClientCertificatePath:     data.ClientCertificatePath.ValueString(),
		ClientCertificatePassword: data.ClientCertificatePassword.ValueString(),
		OIDCTokenFilePath:         data.OIDCTokenFilePath.ValueString(),
		ConnectionString:          data.ConnectionString.ValueString(),
	}
	if !data.Hostname.IsUnknown() {
		raw.Hostname = data.Hostname.ValueString()
	}
	if !data.UseOIDC.IsNull() {
		v := data.UseOIDC.ValueBool()
		raw.UseOIDC = &v
	}
	if !data.UseMSI.IsNull() {
		v := data.UseMSI.ValueBool()
		raw.UseMSI = &v
	}
	if !data.UseCLI.IsNull() {
		v := data.UseCLI.ValueBool()
		raw.UseCLI = &v
	}

	settings, err := resolve(raw, nil)
	if err != nil {
		resp.Diagnostics.AddError("Invalid provider configuration", err.Error())
		return
	}

	factory, err := newClientFactory(settings, p.version)
	if err != nil {
		resp.Diagnostics.AddError("Cannot initialise IoT Hub client", err.Error())
		return
	}

	pd := &common.ProviderData{Settings: settings, HostnameUnknown: data.Hostname.IsUnknown(), Clients: factory}
	tflog.Info(ctx, "configured iothub provider", map[string]any{
		"auth_mode":        settings.Mode.String(),
		"default_hostname": settings.Hostname,
	})

	resp.ResourceData = pd
	resp.DataSourceData = pd
	resp.EphemeralResourceData = pd
	resp.ActionData = pd
	resp.ListResourceData = pd
}

// newClientFactory wires the resolved settings into the service client:
// Entra ID credential or SAS key, provider version for the User-Agent, and
// tflog as the client's debug logger.
func newClientFactory(s common.Settings, version string) (*client.Factory, error) {
	cfg := client.Config{
		Version: version,
		Logger: func(ctx context.Context, msg string, fields map[string]any) {
			tflog.Debug(ctx, msg, fields)
		},
	}
	if s.Mode == common.AuthSAS {
		cfg.SharedAccessKey = &client.SharedAccessKey{
			HostName: s.SAS.HostName, KeyName: s.SAS.SharedAccessKeyName, Key: s.SAS.SharedAccessKey,
		}
	} else {
		cred, err := newEntraCredential(s.Entra)
		if err != nil {
			return nil, err
		}
		cfg.Credential = cred
	}
	return client.NewFactory(cfg)
}

func (p *IoTHubProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		device.NewResource,
		module.NewResource,
		twin.NewDeviceResource,
		twin.NewModuleResource,
		configuration.NewConfigurationResource,
		configuration.NewEdgeDeploymentResource,
	}
}

func (p *IoTHubProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		device.NewDataSource,
		module.NewDataSource,
		module.NewModulesDataSource,
		twin.NewDeviceDataSource,
		twin.NewModuleDataSource,
		twin.NewDigitalTwinDataSource,
		configuration.NewConfigurationDataSource,
		configuration.NewEdgeDeploymentDataSource,
		jobs.NewScheduledJobDataSource,
		jobs.NewImportExportJobDataSource,
		query.NewDataSource,
		statistics.NewDataSource,
	}
}

func (p *IoTHubProvider) EphemeralResources(_ context.Context) []func() ephemeral.EphemeralResource {
	return []func() ephemeral.EphemeralResource{
		device.NewCredentialsEphemeral,
		module.NewCredentialsEphemeral,
		device.NewSASTokenEphemeral,
	}
}

func (p *IoTHubProvider) Actions(_ context.Context) []func() action.Action {
	return []func() action.Action{
		actions.NewDirectMethodAction,
		actions.NewApplyConfigurationAction,
		actions.NewPurgeC2DQueueAction,
		actions.NewScheduledJobAction,
		actions.NewImportExportJobAction,
		actions.NewCancelJobAction,
		actions.NewDigitalTwinCommandAction,
	}
}

func (p *IoTHubProvider) ListResources(_ context.Context) []func() list.ListResource {
	return []func() list.ListResource{
		device.NewListResource,
		module.NewListResource,
		configuration.NewConfigurationListResource,
		configuration.NewEdgeDeploymentListResource,
	}
}

// New returns the provider factory used by main and by tests.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &IoTHubProvider{version: version}
	}
}
