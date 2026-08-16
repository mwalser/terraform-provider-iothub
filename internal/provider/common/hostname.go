package common

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// PublicCloudSuffix is the hostname suffix of every IoT Hub in the Azure
// public cloud — the only cloud this provider addresses (CONCEPT.md §2.3).
const PublicCloudSuffix = ".azure-devices.net"

// ValidateHostname accepts exactly the form the client uses: a bare,
// lowercase, fully-qualified public-cloud IoT Hub hostname. Hostnames are
// case-insensitive DNS names, but Terraform compares strings exactly and
// cannot normalise a configured value, so the provider demands the canonical
// (lowercase) spelling instead of guessing — a mixed-case value would
// otherwise produce "inconsistent result" errors or spurious replacements.
func ValidateHostname(h string) error {
	if h != strings.TrimSpace(h) {
		return fmt.Errorf("hostname %q must not have leading or trailing whitespace", h)
	}
	if strings.Contains(h, "://") || strings.Contains(h, "/") {
		return fmt.Errorf("hostname %q must be a bare host name such as contoso.azure-devices.net, not a URL", h)
	}
	if h != strings.ToLower(h) {
		return fmt.Errorf("hostname %q must be lowercase (hostnames are case-insensitive DNS names and the provider uses the canonical spelling; if the value comes from a hub created with uppercase letters, wrap it in lower())", h)
	}
	if !strings.HasSuffix(h, PublicCloudSuffix) || len(h) == len(PublicCloudSuffix) {
		return fmt.Errorf("hostname %q must end in %s (only the Azure public cloud is supported)", h, PublicCloudSuffix)
	}
	return nil
}

// HostnameValidators is the validator list of the provider's `hostname`.
func HostnameValidators() []validator.String { return []validator.String{hostnameValidator{}} }

type hostnameValidator struct{}

func (hostnameValidator) Description(context.Context) string {
	return "must be a lowercase, fully-qualified public-cloud IoT Hub hostname (contoso.azure-devices.net)"
}

func (v hostnameValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (hostnameValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if err := ValidateHostname(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid IoT Hub hostname", err.Error())
	}
}
