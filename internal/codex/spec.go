package codex

import "croncodex/internal/store"

// SpecFromProfile maps a stored profile onto the Codex invocation settings.
// Keeping the mapping here means the codex package owns every decision about
// how a profile turns into a CLI invocation.
func SpecFromProfile(p store.Profile) ProfileSpec {
	return ProfileSpec{
		Name:            p.Name,
		Model:           p.Model,
		ReasoningEffort: p.ReasoningEffort,
		SandboxMode:     p.SandboxMode,
		ApprovalPolicy:  p.ApprovalPolicy,
		ExtraConfig:     p.ExtraConfig,
		WorkDir:         p.WorkDir,
		IsMinimal:       p.IsMinimal,
	}
}
