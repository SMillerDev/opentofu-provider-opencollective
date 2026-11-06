// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"github.com/SMillerDev/opentofu-provider-opencollective/internal/client"
)

func TestPickMembership(t *testing.T) {
	invitedAdmin := client.Membership{ID: "i-admin", Status: client.MembershipInvited, Role: "ADMIN"}
	activeMember := client.Membership{ID: "a-member", Status: client.MembershipActive, Role: "MEMBER"}
	activeAdmin := client.Membership{ID: "a-admin", Status: client.MembershipActive, Role: "ADMIN"}

	tests := map[string]struct {
		in   []client.Membership
		role string
		want string
	}{
		"none":                       {nil, "ADMIN", ""},
		"role match beats active":    {[]client.Membership{activeMember, invitedAdmin}, "ADMIN", "i-admin"},
		"active beats invited":       {[]client.Membership{invitedAdmin, activeAdmin}, "ADMIN", "a-admin"},
		"fallback when role drifted": {[]client.Membership{invitedAdmin, activeMember}, "ACCOUNTANT", "a-member"},
		"import without role":        {[]client.Membership{invitedAdmin, activeMember}, "", "a-member"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := pickMembership(tc.in, tc.role)
			if (got == nil && tc.want != "") || (got != nil && got.ID != tc.want) {
				t.Errorf("pickMembership() = %+v, want %q", got, tc.want)
			}
		})
	}
}

func TestSameInstant(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"2024-01-01T00:00:00Z", "2024-01-01T00:00:00.000Z", true},
		{"2024-01-01T02:00:00+02:00", "2024-01-01T00:00:00.000Z", true},
		{"2024-01-01T00:00:00Z", "2024-01-01T00:00:01.000Z", false},
		{"not a date", "2024-01-01T00:00:00Z", false},
	}

	for _, tc := range tests {
		if got := sameInstant(tc.a, tc.b); got != tc.want {
			t.Errorf("sameInstant(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
