package lti

import "testing"

func TestParseRoleFamily(t *testing.T) {
	cases := []struct {
		uri  string
		want RoleFamily
	}{
		{"http://purl.imsglobal.org/vocab/lis/v2/membership#Instructor", RoleInstructor},
		{"http://purl.imsglobal.org/vocab/lis/v2/membership#TeachingAssistant", RoleTA},
		{"http://purl.imsglobal.org/vocab/lis/v2/membership#Learner", RoleLearner},
		{"http://purl.imsglobal.org/vocab/lis/v2/membership#Student", RoleLearner},
		{"http://purl.imsglobal.org/vocab/lis/v2/membership#Mentor", RoleOther},
		{"urn:lti:sysrole:ims/lis/Administrator", RoleOther},
		{"", RoleOther},
	}
	for _, tc := range cases {
		if got := ParseRoleFamily(tc.uri); got != tc.want {
			t.Errorf("ParseRoleFamily(%q) = %q, want %q", tc.uri, got, tc.want)
		}
	}
}
