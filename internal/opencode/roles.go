package opencode

const (
	GentleReviewerAgent = "gentle-reviewer"
	GentleWorkerAgent   = "gentle-worker"
)

// DirectRoles is the canonical managed non-SDD role family. These roles are
// deliberately separate from SDD, Judgment Day, and native review lifecycle
// agents so later model and profile work can use the same source of truth.
func DirectRoles() []string {
	return []string{GentleReviewerAgent, GentleWorkerAgent}
}

// DirectRoleKeys returns the managed direct role keys for a profile. The empty
// profile uses the default unsuffixed keys; named profiles use matching suffixes.
func DirectRoleKeys(profile string) []string {
	suffix := ""
	if profile != "" {
		suffix = "-" + profile
	}
	roles := DirectRoles()
	keys := make([]string, len(roles))
	for i, role := range roles {
		keys[i] = role + suffix
	}
	return keys
}

type DirectRoleDefinition struct {
	Description string
	Prompt      string
	Permission  map[string]any
}

// DirectRoleDefinitionFor returns the managed OpenCode definition used by
// generated named profiles. Default overlays carry the same contract as JSON
// assets, while this helper keeps profile generation from inventing a second
// role policy.
func DirectRoleDefinitionFor(name string) (DirectRoleDefinition, bool) {
	var definition DirectRoleDefinition
	switch name {
	case GentleReviewerAgent:
		definition = DirectRoleDefinition{
			Description: "Advisory read-only reviewer for non-SDD issue and PR work",
			Prompt:      "You are Gentle AI's advisory non-SDD reviewer. Audit issue or PR changes, unresolved root cause, scope, and PR boundaries, then report findings only. You are structurally read-only: never edit or write files, ask questions, delegate, or mutate GitHub. Never invoke review, RDD, Judgment Day, or SDD lifecycle commands and never claim lifecycle authority or approval. Return inspected paths, evidence, findings, and blockers. Native explore may prepare broad mapping, but this managed reviewer is the preferred advisory route.",
			Permission:  directReviewerPermission(),
		}
	case GentleWorkerAgent:
		definition = DirectRoleDefinition{
			Description: "Bounded non-SDD delegated-direct implementation worker",
			Prompt:      "You are Gentle AI's bounded non-SDD implementation worker. Execute only the delegated direct task in the current workspace. Do not task, delegate, or ask questions. Do not create SDD artifacts or state, invoke SDD, Judgment Day, RDD, or review lifecycle commands, commit or push, create PRs, or mutate GitHub. Make only the requested bounded edits and run permitted checks. Return changed paths, command and test evidence, and blockers; never claim delivery or approval.",
			Permission:  directWorkerPermission(),
		}
	default:
		return DirectRoleDefinition{}, false
	}
	if !canonicalDirectRoleOwnershipValid(name, definition) {
		return DirectRoleDefinition{}, false
	}
	return definition, true
}

// canonicalDirectRoleOwnershipValid keeps the canonical role contract aligned
// with the ownership primitive. It validates only the source definition; it
// does not attach metadata to generated settings paths.
func canonicalDirectRoleOwnershipValid(name string, definition DirectRoleDefinition) bool {
	entry := map[string]any{
		"mode":        "subagent",
		"hidden":      true,
		"description": definition.Description,
		"prompt":      definition.Prompt,
		"permission":  definition.Permission,
	}
	identity := ManagedAgentIdentity{Owner: ManagedOwner, Component: ManagedComponent, Role: name}
	managed, err := WithManagedMetadata(entry, identity)
	return err == nil && ClassifyOwnership(managed, identity) == OwnershipManaged
}

func directReviewerPermission() map[string]any {
	return map[string]any{
		"read":     map[string]any{"*": "allow"},
		"edit":     "deny",
		"bash":     directBashPermission([]string{"git status*", "git branch --show-current*", "git branch --list*", "git diff*", "git log*", "git show*", "git ls-files*", "git grep*", "gh issue view*", "gh pr view*", "gh pr diff*", "gh pr checks*", "gh repo view*"}),
		"task":     "deny",
		"question": "deny",
	}
}

func directWorkerPermission() map[string]any {
	return map[string]any{
		"read":     map[string]any{"*": "allow"},
		"edit":     "allow",
		"bash":     directBashPermission([]string{"git status*", "git diff*", "git log*", "git show*", "git ls-files*", "git grep*", "gofmt -d*", "go test*", "go build*", "go vet*", "npm test*", "npm run test*", "npm run build*", "pytest*"}),
		"task":     "deny",
		"question": "deny",
	}
}

func directBashPermission(allowed []string) map[string]any {
	permissions := map[string]any{"*": "deny"}
	for _, command := range allowed {
		permissions[command] = "allow"
	}
	return permissions
}
