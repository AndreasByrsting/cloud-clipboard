package db

import (
	"database/sql"
	"strings"
)

func Migrate(database *sql.DB, schema string) error {
	statements := strings.Split(schema, ";")
	for _, statement := range statements {
		stmt := strings.TrimSpace(statement)
		if stmt == "" {
			continue
		}
		if _, err := database.Exec(stmt); err != nil {
			if strings.Contains(err.Error(), "duplicate column name") {
				continue
			}
			return err
		}
	}
	return nil
}
