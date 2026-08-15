package opencode

import (
	"sort"
	"strings"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

type DirectRoleModelSource string

const (
	DirectRoleModelExplicit       DirectRoleModelSource = "explicit"
	DirectRoleModelSemanticPolicy DirectRoleModelSource = "semantic-policy"
	DirectRoleModelCatalogDefault DirectRoleModelSource = "catalog-default"
	DirectRoleModelRuntimeDefault DirectRoleModelSource = "runtime-default"
)

type DirectRoleModelResolution struct {
	Role       string
	Assignment *model.ModelAssignment
	Source     DirectRoleModelSource
	Reason     string
}

// DirectRoleModelRequest is caller-curated so resolution remains pure and does
// not derive direct-role models from SDD assignments or process state.
type DirectRoleModelRequest struct {
	Catalog            map[string]Provider
	AvailableProviders map[string]bool
	Explicit           map[string]model.ModelAssignment
	SemanticCandidates map[string][]model.ModelAssignment
}

type directRoleCandidate struct {
	assignment model.ModelAssignment
	model      Model
}

func ResolveDirectRoleModels(request DirectRoleModelRequest) map[string]DirectRoleModelResolution {
	resolved := make(map[string]DirectRoleModelResolution, len(DirectRoles()))
	for _, role := range DirectRoles() {
		resolved[role] = resolveDirectRoleModel(role, request)
	}
	return resolved
}

func resolveDirectRoleModel(role string, request DirectRoleModelRequest) DirectRoleModelResolution {
	result := DirectRoleModelResolution{Role: role, Source: DirectRoleModelRuntimeDefault, Reason: "runtime-default-no-eligible-model"}
	if explicit, ok := request.Explicit[role]; ok {
		if candidate, valid := validateDirectRoleCandidate(explicit, request); valid {
			return directRoleResolved(role, DirectRoleModelExplicit, "explicit-complete-assignment", candidate)
		}
	}
	candidates := eligibleDirectRoleCandidates(role, request.SemanticCandidates[role], request)
	if len(candidates) > 0 {
		sortDirectRoleCandidates(role, candidates)
		return directRoleResolved(role, DirectRoleModelSemanticPolicy, "semantic-policy-ranked-candidate", candidates[0])
	}
	assignments := make([]model.ModelAssignment, 0)
	for providerID, provider := range request.Catalog {
		for modelID := range provider.Models {
			assignments = append(assignments, model.ModelAssignment{ProviderID: providerID, ModelID: modelID})
		}
	}
	candidates = eligibleDirectRoleCandidates(role, assignments, request)
	if len(candidates) == 0 {
		return result
	}
	sortDirectRoleCandidates(role, candidates)
	return directRoleResolved(role, DirectRoleModelCatalogDefault, "catalog-ranked-eligible-model", candidates[0])
}

func directRoleResolved(role string, source DirectRoleModelSource, reason string, candidate directRoleCandidate) DirectRoleModelResolution {
	assignment := candidate.assignment
	return DirectRoleModelResolution{Role: role, Assignment: &assignment, Source: source, Reason: reason}
}

func eligibleDirectRoleCandidates(role string, assignments []model.ModelAssignment, request DirectRoleModelRequest) []directRoleCandidate {
	candidates := make([]directRoleCandidate, 0, len(assignments))
	for _, assignment := range assignments {
		candidate, valid := validateDirectRoleCandidate(assignment, request)
		if valid && (role != GentleReviewerAgent || candidate.model.Reasoning) {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func validateDirectRoleCandidate(assignment model.ModelAssignment, request DirectRoleModelRequest) (directRoleCandidate, bool) {
	if strings.TrimSpace(assignment.ProviderID) == "" || strings.TrimSpace(assignment.ModelID) == "" || !request.AvailableProviders[assignment.ProviderID] {
		return directRoleCandidate{}, false
	}
	provider, ok := request.Catalog[assignment.ProviderID]
	if !ok || (provider.ID != "" && provider.ID != assignment.ProviderID) {
		return directRoleCandidate{}, false
	}
	catalogModel, ok := provider.Models[assignment.ModelID]
	if !ok || (catalogModel.ID != "" && catalogModel.ID != assignment.ModelID) || !catalogModel.ToolCall {
		return directRoleCandidate{}, false
	}
	return directRoleCandidate{assignment: assignment, model: catalogModel}, true
}

func sortDirectRoleCandidates(role string, candidates []directRoleCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.model.Reasoning != b.model.Reasoning {
			if role == GentleReviewerAgent {
				return a.model.Reasoning
			}
			return !a.model.Reasoning
		}
		if a.model.Limit.Context != b.model.Limit.Context {
			return a.model.Limit.Context > b.model.Limit.Context
		}
		if a.model.Limit.Output != b.model.Limit.Output {
			return a.model.Limit.Output > b.model.Limit.Output
		}
		acost, bcost := a.model.Cost.Input+a.model.Cost.Output, b.model.Cost.Input+b.model.Cost.Output
		if acost != bcost {
			return acost < bcost
		}
		return a.assignment.FullID() < b.assignment.FullID()
	})
}
