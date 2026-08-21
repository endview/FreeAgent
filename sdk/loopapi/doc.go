// Package loopapi defines the dependency-light public contract for the single
// FreeAgent Universal Loop.
//
// Loop is a Core execution boundary, not a Module Kind. Implementing Loop does
// not register a module, select a provider, grant a permission, or authorize an
// external effect. RunInput and RunResult are in-process values rather than a
// persistence or network wire format.
package loopapi
