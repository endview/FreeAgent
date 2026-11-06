package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// ProvisionExtension creates exactly one separate restricted data domain.
// It is explicit maintenance, never called during ordinary context reads.
func ProvisionExtension(ctx context.Context, data, path string) error {
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
	if _, err = domain.ExecContext(ctx, `CREATE SCHEMA aw AUTHORIZATION `+role); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	c := Connection{Host: core.Host, Port: core.Port, User: role, Password: password, Database: database, CoreID: core.CoreID, Bin: core.Bin}
	if err = Write(path, c); err != nil {
		return err
	}
	complete = true
	return nil
}
