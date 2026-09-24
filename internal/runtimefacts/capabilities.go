// Package runtimefacts owns the explicit, reviewable inputs and deterministic
// generation of current runtime facts. Capability maturity is declared here;
// the generator never infers it from code or test coverage.
package runtimefacts

type CapabilityFact struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

var Capabilities = []CapabilityFact{
	{ID: "core.unique-runtime-store", Status: "accepted"},
	{ID: "core.pure-chat", Status: "accepted"},
	{ID: "core.context-compiler", Status: "accepted"},
	{ID: "core.model-profile", Status: "accepted"},
	{ID: "module.rag-local-readonly", Status: "accepted"},
	{ID: "module.memory-local-bounded", Status: "accepted"},
	{ID: "core.action-gateway", Status: "accepted"},
	{ID: "extension.mcp-local-stdio-tool", Status: "accepted"},
	{ID: "runtime.remote-action-http", Status: "accepted"},
	{ID: "runtime.wasm-action-host", Status: "accepted"},
	{ID: "channel.loopback-workspace", Status: "accepted"},
	{ID: "agent.composite-depth1", Status: "accepted"},
	{ID: "scheduler.local-fair", Status: "accepted"},
	{ID: "agent.reviewer-results-gate", Status: "accepted"},
	{ID: "runtime.exact-adapter-lazy", Status: "accepted"},
	{ID: "core.complete-backup-recovery", Status: "accepted"},
	{ID: "core.explicit-schema-migration", Status: "accepted"},
	{ID: "core.effect-ledger-usage", Status: "accepted"},
	{ID: "module.package-conformance", Status: "experimental"},
	{ID: "module.supply-contracts-v1", Status: "accepted"},
	{ID: "module.discovery-snapshot-v1", Status: "accepted"},
	{ID: "module.artifact-ingress-v1", Status: "accepted"},
	{ID: "module.upgrade-review-v1", Status: "accepted"},
	{ID: "module.upgrade-apply-v1", Status: "accepted"},
	{ID: "module.assembly-local-v1", Status: "accepted"},
	{ID: "operator.module-apply", Status: "experimental"},
	{ID: "module.workspace-channel-apply", Status: "accepted"},
	{ID: "module.deepseek-model-replacement", Status: "accepted"},
	{ID: "module.document-insight-dual-port", Status: "accepted"},
	{ID: "provider.deepseek-s3c", Status: "unverified"},
	{ID: "provider.deepseek-controlled", Status: "accepted"},
	{ID: "provider.zhipu-controlled", Status: "accepted"},
	{ID: "product.conversation-config", Status: "accepted"},
	{ID: "module.general-assembly", Status: "planned"},
	{ID: "knowledge.proposal-store", Status: "accepted"},
	{ID: "knowledge.materialized-version", Status: "accepted"},
	{ID: "knowledge.learning-cycle", Status: "accepted"},
	{ID: "collaboration.decision-repair-bounded", Status: "accepted"},
	{ID: "collaboration.cross-workspace", Status: "accepted"},
	{ID: "control.api-contract-v1", Status: "accepted"},
	{ID: "control-plane.online", Status: "accepted"},
	{ID: "release.public-beta", Status: "planned"},
}
