package actions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	defaultJobTimeout    = time.Hour
	defaultActionTimeout = 10 * time.Minute
	pollInterval         = 5 * time.Second
)

// timeoutAttribute is the explicit deadline of an action (actions have no
// timeouts block); covers says what the deadline spans for this action.
func timeoutAttribute(def, covers string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Overall deadline for the invocation, for example `30m` (default `" + def + "`). " + covers,
		Optional:            true,
	}
}

func waitAttribute() schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: "Wait for the job to finish and fail if it failed or was cancelled (default `true`). With `false` the action returns as soon as the job is created.",
		Optional:            true,
	}
}

// parseTimeout returns the configured or default timeout.
func parseTimeout(v types.String, def time.Duration) (time.Duration, diag.Diagnostics) {
	var diags diag.Diagnostics
	if v.IsNull() || v.IsUnknown() {
		return def, diags
	}
	d, err := time.ParseDuration(v.ValueString())
	if err != nil || d <= 0 {
		diags.AddAttributeError(path.Root("timeout"), "Invalid timeout", "Use a positive Go duration such as \"30m\" or \"2h\".")
		return 0, diags
	}
	return d, diags
}

func boolOr(v types.Bool, def bool) bool {
	if v.IsNull() || v.IsUnknown() {
		return def
	}
	return v.ValueBool()
}

// newJobID generates a job ID when the configuration leaves it out.
func newJobID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}

// sleepCtx sleeps or returns early when ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
