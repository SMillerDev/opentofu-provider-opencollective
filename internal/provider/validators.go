// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// rfc3339Validator checks that a string is an RFC 3339 timestamp.
type rfc3339Validator struct{}

var _ validator.String = rfc3339Validator{}

func (rfc3339Validator) Description(context.Context) string {
	return "value must be an RFC 3339 timestamp, e.g. 2024-01-31T00:00:00Z"
}

func (v rfc3339Validator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v rfc3339Validator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	if _, err := time.Parse(time.RFC3339, req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid timestamp", v.Description(ctx)+": "+err.Error())
	}
}

// sameInstant reports whether a and b are RFC 3339 timestamps for the same
// instant, e.g. "2024-01-01T00:00:00Z" and "2024-01-01T00:00:00.000Z".
func sameInstant(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339Nano, a)
	tb, errB := time.Parse(time.RFC3339Nano, b)

	return errA == nil && errB == nil && ta.Equal(tb)
}
