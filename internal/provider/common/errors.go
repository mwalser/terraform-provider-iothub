package common

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
)

// DescribeError turns a client error into diagnostic detail text with the
// hint a practitioner needs (CONCEPT.md §11.4).
func DescribeError(err error) string {
	e, ok := client.AsError(err)
	if !ok {
		return err.Error()
	}
	var b strings.Builder
	b.WriteString(e.Error())
	switch e.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		if client.IsModuleOnDisabledDevice(err) {
			break // the message already explains the service quirk; the RBAC hint would mislead
		}
		if e.SASAuth {
			b.WriteString("\n\nThe shared access policy in connection_string is not accepted for this operation. Check that the " +
				"policy still exists, that its key is current, and that it grants the needed permissions (RegistryRead/RegistryWrite " +
				"for identities, ServiceConnect for twins, methods, jobs and configurations).")
		} else {
			b.WriteString("\n\nThe identity has no data-plane permission on this hub. Assign an IoT Hub data role at hub scope " +
				"(IoT Hub Data Contributor, or the narrower Registry/Twin Contributor or Data Reader roles); " +
				"Owner and Contributor do not include data-plane actions.")
		}
	case http.StatusTooManyRequests:
		what := "the request"
		if e.Operation != "" {
			what = e.Operation + " requests"
		}
		b.WriteString("\n\nThe hub throttled " + what + " for the whole operation timeout. Throttling limits scale with the " +
			"hub's tier and unit count; consider more units, spreading applies, or -parallelism.")
	case http.StatusPreconditionFailed:
		b.WriteString("\n\nThe object changed outside Terraform since the last refresh. Run `terraform plan` again.")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		b.WriteString("\n\nThe operation timeout was reached while retrying.")
	}
	return b.String()
}
