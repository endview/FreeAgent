package runtimefacts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/endview/freeagent/internal/currentstore"
	_ "modernc.org/sqlite"
)

const SchemaVersion = "freeagent-runtime-facts/v1"

var capabilityIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]+$`)

type Facts struct {
	SchemaVersion string          `json:"schema_version"`
	Version       VersionFacts    `json:"version"`
	Store         StoreFacts      `json:"current_store"`
	Migration     MigrationFacts  `json:"migration"`
	Packages      PackageFacts    `json:"packages"`
	Capabilities  CapabilityFacts `json:"capabilities"`
}

type VersionFacts struct {
	Release string `json:"release"`
}

type StoreFacts struct {
	SchemaIdentity    string `json:"schema_identity"`
	UserVersion       int    `json:"user_version"`
	SchemaFingerprint string `json:"schema_fingerprint"`
	Tables            int    `json:"tables"`
	ExplicitIndexes   int    `json:"explicit_indexes"`
	Triggers          int    `json:"triggers"`
}

type MigrationFacts struct {
	Name   string `json:"name"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type PackageFacts struct {
	Source                      int `json:"source"`
	ProductionDependencyClosure int `json:"production_dependency_closure"`
}

type CapabilityFacts struct {
	Count   int              `json:"count"`
	Entries []CapabilityFact `json:"entries"`
}

func Generate(ctx context.Context, root string) ([]byte, []byte, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, fmt.Errorf("runtimefacts: resolve root: %w", err)
	}
	version, err := readVersion(root)
	if err != nil {
		return nil, nil, err
	}
	bootstrap, err := currentstore.Migration0001()
	if err != nil {
		return nil, nil, err
	}
	currentMigration, err := currentstore.Migration0002()
	if err != nil {
		return nil, nil, err
	}
	migrationSet := append(append([]byte(nil), bootstrap...), currentMigration...)
	storeFacts, err := inspectSchema(ctx, bootstrap, currentMigration)
	if err != nil {
		return nil, nil, err
	}
	sourcePackages, err := goPackageCount(ctx, root, "list", "./...")
	if err != nil {
		return nil, nil, err
	}
	productionPackages, err := goPackageCount(
		ctx,
		root,
		"list",
		"-deps",
		"./cmd/freeagent",
	)
	if err != nil {
		return nil, nil, err
	}
	capabilities, err := validatedCapabilities()
	if err != nil {
		return nil, nil, err
	}
	migrationDigest := sha256.Sum256(migrationSet)
	facts := Facts{
		SchemaVersion: SchemaVersion,
		Version:       VersionFacts{Release: version},
		Store:         storeFacts,
		Migration: MigrationFacts{
			Name:   "0001_current.sql + 0002_server_owned_review.sql",
			Bytes:  len(migrationSet),
			SHA256: hex.EncodeToString(migrationDigest[:]),
		},
		Packages: PackageFacts{
			Source:                      sourcePackages,
			ProductionDependencyClosure: productionPackages,
		},
		Capabilities: CapabilityFacts{
			Count:   len(capabilities),
			Entries: capabilities,
		},
	}
	jsonBytes, err := json.MarshalIndent(facts, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("runtimefacts: encode JSON: %w", err)
	}
	jsonBytes = append(jsonBytes, '\n')
	return jsonBytes, renderMarkdown(facts), nil
}

func inspectSchema(
	ctx context.Context,
	migrations ...[]byte,
) (StoreFacts, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return StoreFacts{}, fmt.Errorf("runtimefacts: open schema database: %w", err)
	}
	defer db.Close()
	for _, migration := range migrations {
		if _, err := db.ExecContext(ctx, string(migration)); err != nil {
			return StoreFacts{}, fmt.Errorf("runtimefacts: apply migration: %w", err)
		}
	}
	objects, err := currentstore.LoadSchemaObjects(ctx, db)
	if err != nil {
		return StoreFacts{}, err
	}
	fingerprint, err := currentstore.ComputeSchemaFingerprint(objects)
	if err != nil {
		return StoreFacts{}, err
	}
	if fingerprint != currentstore.ExpectedSchemaFingerprint {
		return StoreFacts{}, fmt.Errorf(
			"runtimefacts: schema fingerprint %s differs from %s",
			fingerprint,
			currentstore.ExpectedSchemaFingerprint,
		)
	}
	facts := StoreFacts{
		SchemaIdentity:    currentstore.SchemaIdentity,
		UserVersion:       currentstore.UserVersion,
		SchemaFingerprint: fingerprint,
	}
	for _, object := range objects {
		switch object.Type {
		case "table":
			facts.Tables++
		case "index":
			facts.ExplicitIndexes++
		case "trigger":
			facts.Triggers++
		}
	}
	return facts, nil
}

func readVersion(root string) (string, error) {
	content, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return "", fmt.Errorf("runtimefacts: read VERSION: %w", err)
	}
	version := strings.TrimSpace(string(content))
	if version == "" || strings.ContainsAny(version, "\r\n\t ") {
		return "", fmt.Errorf("runtimefacts: VERSION must contain one token")
	}
	return version, nil
}

func goPackageCount(
	ctx context.Context,
	root string,
	arguments ...string,
) (int, error) {
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	command := exec.CommandContext(ctx, goBinary, arguments...)
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return 0, fmt.Errorf("runtimefacts: go %s: %w", strings.Join(arguments, " "), err)
	}
	modulePath := "github.com/endview/freeagent"
	count := 0
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		path := strings.TrimSpace(string(line))
		if path == modulePath || strings.HasPrefix(path, modulePath+"/") {
			count++
		}
	}
	if count == 0 {
		return 0, fmt.Errorf("runtimefacts: go %s returned no module packages", strings.Join(arguments, " "))
	}
	return count, nil
}

func validatedCapabilities() ([]CapabilityFact, error) {
	result := append([]CapabilityFact(nil), Capabilities...)
	sort.Slice(result, func(left, right int) bool {
		return result[left].ID < result[right].ID
	})
	for index, capability := range result {
		if !capabilityIDPattern.MatchString(capability.ID) {
			return nil, fmt.Errorf("runtimefacts: invalid capability ID %q", capability.ID)
		}
		if index > 0 && result[index-1].ID == capability.ID {
			return nil, fmt.Errorf("runtimefacts: duplicate capability ID %q", capability.ID)
		}
		switch capability.Status {
		case "accepted", "experimental", "unverified", "planned":
		default:
			return nil, fmt.Errorf(
				"runtimefacts: invalid capability status %q for %q",
				capability.Status,
				capability.ID,
			)
		}
	}
	return result, nil
}

func renderMarkdown(facts Facts) []byte {
	var output strings.Builder
	output.WriteString("<!-- Code generated by internal/tools/genfacts; DO NOT EDIT. -->\n\n")
	output.WriteString("## Runtime Facts\n\n")
	fmt.Fprintf(&output, "- Version: `%s`\n", facts.Version.Release)
	fmt.Fprintf(
		&output,
		"- Current Store: `%d` tables, `%d` explicit indexes, `%d` triggers\n",
		facts.Store.Tables,
		facts.Store.ExplicitIndexes,
		facts.Store.Triggers,
	)
	fmt.Fprintf(&output, "- Schema fingerprint: `%s`\n", facts.Store.SchemaFingerprint)
	fmt.Fprintf(
		&output,
		"- Migration: `%s`, `%d` bytes, SHA-256 `%s`\n",
		facts.Migration.Name,
		facts.Migration.Bytes,
		facts.Migration.SHA256,
	)
	fmt.Fprintf(
		&output,
		"- Go packages: `%d` source, `%d` production dependency closure\n",
		facts.Packages.Source,
		facts.Packages.ProductionDependencyClosure,
	)
	fmt.Fprintf(&output, "- Declared capabilities: `%d`\n\n", facts.Capabilities.Count)
	output.WriteString("| Capability | Declared status |\n")
	output.WriteString("| --- | --- |\n")
	for _, capability := range facts.Capabilities.Entries {
		fmt.Fprintf(&output, "| `%s` | `%s` |\n", capability.ID, capability.Status)
	}
	return []byte(output.String())
}
