// Package provider implements the Terraform provider for the Azure IoT Hub
// data plane. Design and decisions: CONCEPT.md at the repository root.
package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/list"
	"github.com/hashicorp/terraform-plugin-framework/path"
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
	ClientIDFilePath          types.String `tfsdk:"client_id_file_path"`
	ClientSecret              types.String `tfsdk:"client_secret"`
	ClientSecretFilePath      types.String `tfsdk:"client_secret_file_path"`
	ClientCertificatePath     types.String `tfsdk:"client_certificate_path"`
	ClientCertificate         types.String `tfsdk:"client_certificate"`
	ClientCertificatePassword types.String `tfsdk:"client_certificate_password"`
	UseOIDC                   types.Bool   `tfsdk:"use_oidc"`
	UseAKSWorkloadIdentity    types.Bool   `tfsdk:"use_aks_workload_identity"`
	OIDCToken                 types.String `tfsdk:"oidc_token"`
	OIDCTokenFilePath         types.String `tfsdk:"oidc_token_file_path"`
	OIDCRequestURL            types.String `tfsdk:"oidc_request_url"`
	OIDCRequestToken          types.String `tfsdk:"oidc_request_token"`
	ADOPipelineServiceConnID  types.String `tfsdk:"ado_pipeline_service_connection_id"`
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
			"automatic device management configurations, IoT Edge deployments, jobs, direct methods and Plug and Play. " +
			"The hub itself, and everything else under Azure Resource Manager, is managed with the `azurerm` provider.\n\n" +
			"Requirements: Terraform 1.14 or later, an IoT Hub in the Azure public cloud (no sovereign clouds), and an " +
			"Entra ID identity with an IoT Hub data-plane role on the hub or a shared access policy connection string.\n\n" +
			"Not covered: sending cloud-to-device messages, receiving feedback or file-upload notifications, file upload, " +
			"and Device Provisioning Service enrollments.",
		Attributes: map[string]schema.Attribute{
			"hostname": schema.StringAttribute{
				MarkdownDescription: "Hostname of the IoT Hub, in lowercase, for example `contoso.azure-devices.net`. Falls back to " +
					"`IOTHUB_HOSTNAME`. Optional when `connection_string` is set: it is then taken from the connection string, " +
					"and both must name the same hub if given.",
				Optional:   true,
				Validators: common.HostnameValidators(),
			},
			"tenant_id": schema.StringAttribute{
				MarkdownDescription: "Entra ID tenant. Falls back to `ARM_TENANT_ID` or `AZURE_TENANT_ID`.",
				Optional:            true,
			},
			"client_id": schema.StringAttribute{
				MarkdownDescription: "Entra ID application (client) ID. Falls back to `ARM_CLIENT_ID` or `AZURE_CLIENT_ID`.",
				Optional:            true,
			},
			"client_id_file_path": schema.StringAttribute{
				MarkdownDescription: "File containing the client ID. Falls back to `ARM_CLIENT_ID_FILE_PATH`.",
				Optional:            true,
			},
			"client_secret": schema.StringAttribute{
				MarkdownDescription: "Client secret. Falls back to `ARM_CLIENT_SECRET` or `AZURE_CLIENT_SECRET`.",
				Optional:            true,
				Sensitive:           true,
			},
			"client_secret_file_path": schema.StringAttribute{
				MarkdownDescription: "File containing the client secret. Falls back to `ARM_CLIENT_SECRET_FILE_PATH`.",
				Optional:            true,
			},
			"client_certificate_path": schema.StringAttribute{
				MarkdownDescription: "Path to the client certificate: a PKCS#12 file (`.pfx`) or a PEM bundle with the certificate and an unencrypted private key. Falls back to `ARM_CLIENT_CERTIFICATE_PATH` or `AZURE_CLIENT_CERTIFICATE_PATH`.",
				Optional:            true,
			},
			"client_certificate": schema.StringAttribute{
				MarkdownDescription: "The client certificate itself, base64 encoded (PKCS#12 or PEM). Falls back to `ARM_CLIENT_CERTIFICATE`.",
				Optional:            true,
				Sensitive:           true,
			},
			"client_certificate_password": schema.StringAttribute{
				MarkdownDescription: "Password of the client certificate, if any. Falls back to `ARM_CLIENT_CERTIFICATE_PASSWORD` or `AZURE_CLIENT_CERTIFICATE_PASSWORD`.",
				Optional:            true,
				Sensitive:           true,
			},
			"use_oidc": schema.BoolAttribute{
				MarkdownDescription: "Authenticate with a federated (OIDC) token. It comes from Azure DevOps when " +
					"`ado_pipeline_service_connection_id` is set, otherwise from `oidc_token`, `oidc_token_file_path`, or " +
					"`oidc_request_url` and `oidc_request_token`, in that order. Falls back to `ARM_USE_OIDC`.",
				Optional: true,
			},
			"use_aks_workload_identity": schema.BoolAttribute{
				MarkdownDescription: "The same as `use_oidc`, under azurerm's name for AKS workload identity. Falls back to " +
					"`ARM_USE_AKS_WORKLOAD_IDENTITY`.",
				Optional: true,
			},
			"oidc_token": schema.StringAttribute{
				MarkdownDescription: "The federated token itself. Falls back to `ARM_OIDC_TOKEN`.",
				Optional:            true,
				Sensitive:           true,
			},
			"oidc_token_file_path": schema.StringAttribute{
				MarkdownDescription: "File containing the federated token, read on every request. Falls back to `ARM_OIDC_TOKEN_FILE_PATH` or `AZURE_FEDERATED_TOKEN_FILE`.",
				Optional:            true,
			},
			"oidc_request_url": schema.StringAttribute{
				MarkdownDescription: "URL of the token request endpoint of GitHub Actions or Azure Pipelines. Falls back to `ARM_OIDC_REQUEST_URL`, `ACTIONS_ID_TOKEN_REQUEST_URL` or `SYSTEM_OIDCREQUESTURI`.",
				Optional:            true,
			},
			"oidc_request_token": schema.StringAttribute{
				MarkdownDescription: "Bearer token for `oidc_request_url`: the GitHub Actions request token or the Azure Pipelines system access token. Falls back to `ARM_OIDC_REQUEST_TOKEN`, `ACTIONS_ID_TOKEN_REQUEST_TOKEN` or `SYSTEM_ACCESSTOKEN`.",
				Optional:            true,
				Sensitive:           true,
			},
			"ado_pipeline_service_connection_id": schema.StringAttribute{
				MarkdownDescription: "Azure DevOps service connection ID for workload identity federation. Falls back to `ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID` or `ARM_OIDC_AZURE_SERVICE_CONNECTION_ID`.",
				Optional:            true,
			},
			"use_msi": schema.BoolAttribute{
				MarkdownDescription: "Authenticate with the managed identity of the machine running Terraform (default `false`). Set `client_id` for a user-assigned identity. Falls back to `ARM_USE_MSI`.",
				Optional:            true,
			},
			"use_cli": schema.BoolAttribute{
				MarkdownDescription: "Use the Azure CLI login when no other method is configured (default `true`). Falls back to `ARM_USE_CLI`.",
				Optional:            true,
			},
			"connection_string": schema.StringAttribute{
				MarkdownDescription: "Connection string of a hub shared access policy (`HostName=…;SharedAccessKeyName=…;SharedAccessKey=…`). " +
					"Setting it selects SAS authentication instead of Entra ID. Falls back to `IOTHUB_CONNECTION_STRING`.",
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

	// hostname and connection_string may be unknown during the first plan of
	// a configuration that also creates the hub (azurerm_iothub.x.hostname,
	// azurerm_iothub_shared_access_policy.x.primary_connection_string). The
	// provider is configured again with the real values at apply time; until
	// then constructs defer or return unknown values. Everything else must be
	// known to authenticate.
	mustBeKnown := []struct {
		name string
		v    interface{ IsUnknown() bool }
	}{
		{"tenant_id", data.TenantID}, {"client_id", data.ClientID}, {"client_id_file_path", data.ClientIDFilePath},
		{"client_secret", data.ClientSecret}, {"client_secret_file_path", data.ClientSecretFilePath},
		{"client_certificate_path", data.ClientCertificatePath}, {"client_certificate", data.ClientCertificate},
		{"client_certificate_password", data.ClientCertificatePassword}, {"use_oidc", data.UseOIDC},
		{"use_aks_workload_identity", data.UseAKSWorkloadIdentity}, {"oidc_token", data.OIDCToken},
		{"oidc_token_file_path", data.OIDCTokenFilePath}, {"oidc_request_url", data.OIDCRequestURL}, {"oidc_request_token", data.OIDCRequestToken},
		{"ado_pipeline_service_connection_id", data.ADOPipelineServiceConnID}, {"use_msi", data.UseMSI}, {"use_cli", data.UseCLI},
	}
	for _, a := range mustBeKnown {
		if a.v.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(a.name), "Unknown provider configuration value",
				"The provider cannot authenticate while \""+a.name+"\" is unknown. Give it a value that is known at plan "+
					"time (a literal, variable or output of an already-applied resource); only \"hostname\" and "+
					"\"connection_string\" may be unknown.")
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	pd := &common.ProviderData{}
	resp.ResourceData = pd
	resp.DataSourceData = pd
	resp.EphemeralResourceData = pd
	resp.ActionData = pd
	resp.ListResourceData = pd

	if data.ConnectionString.IsUnknown() {
		tflog.Info(ctx, "iothub provider: connection_string is not known yet; the hub is addressed at apply time")
		return
	}

	raw := rawConfig{
		TenantID:                  data.TenantID.ValueString(),
		ClientID:                  data.ClientID.ValueString(),
		ClientIDFilePath:          data.ClientIDFilePath.ValueString(),
		ClientSecret:              data.ClientSecret.ValueString(),
		ClientSecretFilePath:      data.ClientSecretFilePath.ValueString(),
		ClientCertificatePath:     data.ClientCertificatePath.ValueString(),
		ClientCertificate:         data.ClientCertificate.ValueString(),
		ClientCertificatePassword: data.ClientCertificatePassword.ValueString(),
		OIDCToken:                 data.OIDCToken.ValueString(),
		OIDCTokenFilePath:         data.OIDCTokenFilePath.ValueString(),
		OIDCRequestURL:            data.OIDCRequestURL.ValueString(),
		OIDCRequestToken:          data.OIDCRequestToken.ValueString(),
		ADOPipelineServiceConnID:  data.ADOPipelineServiceConnID.ValueString(),
		ConnectionString:          data.ConnectionString.ValueString(),
	}
	if !data.Hostname.IsUnknown() {
		raw.Hostname = data.Hostname.ValueString()
	}
	if !data.UseOIDC.IsNull() {
		v := data.UseOIDC.ValueBool()
		raw.UseOIDC = &v
	}
	if !data.UseAKSWorkloadIdentity.IsNull() {
		v := data.UseAKSWorkloadIdentity.ValueBool()
		raw.UseAKSWorkloadIdentity = &v
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
	pd.Settings = settings

	if data.Hostname.IsUnknown() {
		tflog.Info(ctx, "iothub provider: hostname is not known yet; the hub is addressed at apply time")
		return
	}
	if settings.Hostname == "" {
		resp.Diagnostics.AddError("No IoT Hub hostname configured",
			"Set `hostname` or `connection_string` on the provider block, or the IOTHUB_HOSTNAME or IOTHUB_CONNECTION_STRING environment variable.")
		return
	}

	c, err := newClient(settings, p.version)
	if err != nil {
		resp.Diagnostics.AddError("Cannot initialise IoT Hub client", err.Error())
		return
	}
	pd.Client = c
	tflog.Info(ctx, "configured iothub provider", map[string]any{
		"auth_mode": settings.Mode.String(),
		"hostname":  settings.Hostname,
	})
}

// newClient wires the resolved settings into the service client: Entra ID
// credential or SAS key, provider version for the User-Agent, and tflog as
// the client's debug logger.
// NewClientFromEnvironment builds a client for hostname with the credentials
// the provider itself resolves from the environment (ARM_*/AZURE_* variables
// or IOTHUB_CONNECTION_STRING). The acceptance harness uses it for its
// out-of-band reads and checks, so those follow the same rules as the
// provider under test (use_cli, no probing of IMDS or other sources).
func NewClientFromEnvironment(hostname, version string) (*client.Client, error) {
	s, err := resolve(rawConfig{Hostname: hostname}, nil)
	if err != nil {
		return nil, err
	}
	return newClient(s, version)
}

func newClient(s common.Settings, version string) (*client.Client, error) {
	cfg := client.Config{
		Hostname: s.Hostname,
		Version:  version,
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
	return client.New(cfg)
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
		actions.NewSetEdgeModulesAction,
		actions.NewPurgeC2DQueueAction,
		actions.NewScheduledJobAction,
		actions.NewImportExportJobAction,
		actions.NewCancelJobAction,
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
