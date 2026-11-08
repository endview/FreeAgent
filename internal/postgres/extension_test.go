package postgres

import "testing"

func TestExtensionSchemaDeclaration(t *testing.T) {
	for _, name := range []string{"fa", "fa_records", "public", "information_schema", "pg_catalog", "pg_temp_1", "a;DROP SCHEMA fa", "a.b", "A", "", "a-b", "schema\nname"} {
		if ValidExtensionSchema(name) {
			t.Fatal("unsafe schema accepted", name)
		}
	}
	for _, name := range []string{"aw", "project_labels", "x2"} {
		if !ValidExtensionSchema(name) {
			t.Fatal("valid schema rejected", name)
		}
	}
}
