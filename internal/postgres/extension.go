package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ProvisionExtension creates exactly one separate restricted data domain.
// It is explicit maintenance, never called during ordinary context reads.
func ProvisionExtension(ctx context.Context, data, path, extensionID, schema, storageVersion string) error {
	if extensionID == "" || !ValidExtensionSchema(schema) || storageVersion == "" || len(storageVersion) > 64 {
		return errors.New("postgres: explicit extension storage declaration required")
	}
	core, err := Read(filepath.Join(data, ConfigFile))
	if err != nil {
		return err
	}
	adminPath := filepath.Join(data, "postgres-maintenance.json")
	if service := os.Getenv(ServiceFileEnv); service != "" {
		adminPath = service
	}
	admin, err := Read(adminPath)
	if err != nil {
		return errors.New("postgres: explicit maintenance connection required to initialize extension storage")
	}
	if admin.Host != core.Host || admin.Port != core.Port {
		return errors.New("postgres: extension must use the Core PostgreSQL instance")
	}
	if _, err = os.Lstat(path); err == nil {
		return errors.New("postgres: extension storage already initialized")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	db, err := SQL(admin, "", false)
	if err != nil {
		return err
	}
	defer db.Close()
	id, password := ID(), ID()+ID()
	role, database := "fa_ext_"+id, "fa_ext_"+id
	if _, err = db.ExecContext(ctx, `CREATE ROLE `+role+` LOGIN PASSWORD '`+password+`' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT`); err != nil {
		return errors.New("postgres: cannot create extension role")
	}
	created, complete := false, false
	defer func() {
		if !complete {
			if created {
				_, _ = db.ExecContext(context.Background(), `DROP DATABASE `+database)
			}
			_, _ = db.ExecContext(context.Background(), `DROP ROLE `+role)
		}
	}()
	if _, err = db.ExecContext(ctx, `CREATE DATABASE `+database+` TEMPLATE template0 ENCODING 'UTF8'`); err != nil {
		return errors.New("postgres: cannot create extension database")
	}
	created = true
	for _, q := range []string{`REVOKE ALL ON DATABASE ` + database + ` FROM PUBLIC`, `GRANT CONNECT ON DATABASE ` + database + ` TO ` + role} {
		if _, err = db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	admin.Database = database
	domain, err := SQL(admin, "", false)
	if err != nil {
		return err
	}
	defer domain.Close()
	if _, err = domain.ExecContext(ctx, `REVOKE CREATE ON SCHEMA public FROM PUBLIC`); err != nil {
		return err
	}
	if _, err = domain.ExecContext(ctx, `CREATE SCHEMA `+schema+` AUTHORIZATION `+role); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	c := Connection{ExtensionID: extensionID, Schema: schema, StorageVersion: storageVersion, Host: core.Host, Port: core.Port, User: role, Password: password, Database: database, CoreID: core.CoreID, Bin: core.Bin}
	if err = Write(path, c); err != nil {
		return err
	}
	complete = true
	return nil
}

var extensionSchemaName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

func ValidExtensionSchema(schema string) bool {
	return extensionSchemaName.MatchString(schema) && schema != "fa" && schema != "public" && schema != "information_schema" && !strings.HasPrefix(schema, "pg_") && !strings.HasPrefix(schema, "fa_")
}

// BindExistingExtension is explicit maintenance, never a chat/startup fallback.
// It adds declaration metadata only after verifying the existing restricted domain.
func BindExistingExtension(ctx context.Context, data, path, id, schema, version string) error {
	if !ValidExtensionSchema(schema) || id == "" || version == "" {
		return errors.New("postgres: invalid extension declaration")
	}
	c, err := Read(path)
	if err != nil {
		return err
	}
	core, err := Read(filepath.Join(data, ConfigFile))
	if err != nil {
		return err
	}
	if c.CoreID != core.CoreID || c.Host != core.Host || c.Port != core.Port || c.Database == core.Database || c.User == core.User {
		return errors.New("postgres: extension binding identity mismatch")
	}
	bound := c.ExtensionID != "" || c.Schema != "" || c.StorageVersion != ""
	if bound && (c.ExtensionID != id || c.Schema != schema || c.StorageVersion != version) {
		return errors.New("postgres: extension storage declaration changed")
	}
	db, err := SQL(c, "", false)
	if err != nil {
		return err
	}
	defer db.Close()
	var schemas int
	var owned bool
	if err = db.QueryRowContext(ctx, `SELECT count(*),COALESCE(bool_and(nspname=$1 AND pg_get_userbyid(nspowner)=current_user),false) FROM pg_namespace WHERE nspname NOT IN ('public','information_schema') AND nspname NOT LIKE 'pg\_%' ESCAPE '\'`, schema).Scan(&schemas, &owned); err != nil {
		return err
	}
	if schemas != 1 || !owned {
		return errors.New("postgres: existing extension schema is ambiguous or has a different owner")
	}
	var restricted bool
	if err = db.QueryRowContext(ctx, `SELECT NOT(rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls) FROM pg_roles WHERE rolname=current_user`).Scan(&restricted); err != nil {
		return err
	}
	if !restricted {
		return errors.New("postgres: extension role is not restricted")
	}
	if bound {
		return nil
	}
	c.ExtensionID, c.Schema, c.StorageVersion = id, schema, version
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".binding-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err = temporary.Chmod(0600); err == nil {
		_, err = temporary.Write(append(raw, '\n'))
	}
	if syncErr := temporary.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
