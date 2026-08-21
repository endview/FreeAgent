// Package moduleapi defines FreeAgent's public, versioned module declaration
// and capability contracts.
//
// A package manifest only requests a runtime mode, exact Ports, and
// permissions. Parsing or verifying a package never installs, activates,
// authorizes, binds, loads, or executes it. Core-owned policy assigns the
// ExecutionClass and freezes authority and failure behavior before admission.
// Public Action providers may Describe and Prepare; only the private
// Core Gateway and Module Host execution path can perform a prepared action.
// Supply-chain v1 values are likewise authority-free: parsing a publisher key,
// detached signature, source policy, discovery snapshot, candidate, or
// decision never refreshes a source or changes module/runtime state.
package moduleapi
