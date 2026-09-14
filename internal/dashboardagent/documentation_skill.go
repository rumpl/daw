package dashboardagent

import (
	_ "embed"

	"github.com/docker/docker-agent/pkg/skills"
)

const developerDocumentationSkillName = "atelier-dashboard-plugin-development"

//go:embed developer_documentation.md
var developerDocumentation string

// DeveloperDocumentationSkill exposes Atelier's complete plugin contract as an
// inline skill. Keeping the generated reference in memory makes it available
// in every dashboard session without installing files into the user's global
// skill directories.
func DeveloperDocumentationSkill() skills.Skill {
	return skills.Skill{
		Name:        developerDocumentationSkillName,
		Description: "Create or modify Atelier dashboard plugins using the complete current backend API, plugin runtime, host component, prop, and hook contract.",
		InlineContent: `Use this reference whenever you create or modify an Atelier dashboard plugin. Follow the documented API and runtime contract exactly.

` + developerDocumentation,
	}
}

// dashboardSkills adds Atelier's built-in documentation skill to the skills
// discovered from the user's normal local skill locations. The built-in skill
// wins if a local skill uses the same reserved name.
func dashboardSkills(loaded []skills.Skill) []skills.Skill {
	result := make([]skills.Skill, 0, len(loaded)+1)
	result = append(result, DeveloperDocumentationSkill())
	for _, skill := range loaded {
		if skill.Name != developerDocumentationSkillName {
			result = append(result, skill)
		}
	}
	return result
}
