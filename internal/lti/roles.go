package lti

import "strings"

// RoleFamily is the normalized class of an IMS LTI role URI.
type RoleFamily string

const (
	// RoleInstructor covers teachers/instructors.
	RoleInstructor RoleFamily = "instructor"
	// RoleLearner covers students/learners.
	RoleLearner RoleFamily = "learner"
	// RoleTA covers teaching assistants.
	RoleTA RoleFamily = "ta"
	// RoleOther covers everything else.
	RoleOther RoleFamily = "other"
)

const lisRolePrefix = "http://purl.imsglobal.org/vocab/lis/v2/membership#"

// ParseRoleFamily maps an IMS LIS membership role URI to a role family.
func ParseRoleFamily(roleURI string) RoleFamily {
	switch strings.TrimPrefix(roleURI, lisRolePrefix) {
	case "Instructor", "Teacher":
		return RoleInstructor
	case "TeachingAssistant", "TeacherAssistant":
		return RoleTA
	case "Learner", "Student":
		return RoleLearner
	default:
		return RoleOther
	}
}
