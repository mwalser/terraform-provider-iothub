package device

import "github.com/hashicorp/terraform-plugin-framework/types/basetypes"

// basetypesObjectAsOptions tolerates null/unknown nested values when decoding.
var basetypesObjectAsOptions = basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true, UnhandledUnknownAsEmpty: true}
