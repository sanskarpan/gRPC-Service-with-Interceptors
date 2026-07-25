package storage

import (
	"testing"
)

func TestParseMigrationVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		want    int64
		wantErr bool
	}{
		{"0001_users.sql", 1, false},
		{"0002_user_indexes.sql", 2, false},
		{"0001_users.down.sql", 1, false},
		{"0002_user_indexes.down.sql", 2, false},
		{"0010_add_column.sql", 10, false},
		{"no_prefix.sql", 0, true},
		{".sql", 0, true},
		{"abc.sql", 0, true},
		{"_0001.sql", 0, true},
		{"0001.txt", 0, true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseMigrationVersion(tt.name)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseMigrationVersion(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("parseMigrationVersion(%q) = %d, want %d", tt.name, got, tt.want)
			}
		})
	}
}

func TestEmbeddedMigrationFilesExist(t *testing.T) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		t.Fatalf("ReadDir migrations: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no embedded migration files found")
	}

	var upFiles, downFiles int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatalf("ReadFile %s: %v", entry.Name(), err)
		}
		if len(body) == 0 {
			t.Fatalf("migration file %s is empty", entry.Name())
		}
		if entry.Name()[0] < '0' || entry.Name()[0] > '9' {
			t.Fatalf("migration file %q does not start with a digit", entry.Name())
		}
		switch {
		case len(entry.Name()) > 9 && entry.Name()[len(entry.Name())-9:] == ".down.sql":
			downFiles++
		case len(entry.Name()) > 4 && entry.Name()[len(entry.Name())-4:] == ".sql":
			upFiles++
		}
	}
	if upFiles == 0 {
		t.Fatal("no up migration files found")
	}
	if downFiles == 0 {
		t.Fatal("no down migration files found")
	}
}

func TestEmbeddedMigrationFilesValid(t *testing.T) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		t.Fatalf("ReadDir migrations: %v", err)
	}
	upVersions := make(map[int64]bool)
	downVersions := make(map[int64]bool)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		v, err := parseMigrationVersion(name)
		if err != nil {
			t.Fatalf("parseMigrationVersion(%q): %v", name, err)
		}
		if v < 1 {
			t.Fatalf("migration version %d must be >= 1", v)
		}
		if len(name) > 4 && name[len(name)-4:] == ".sql" {
			if len(name) > 9 && name[len(name)-9:] == ".down.sql" {
				downVersions[v] = true
			} else {
				upVersions[v] = true
			}
		}
	}
	// Check each up version has a corresponding down
	for v := range upVersions {
		if !downVersions[v] {
			t.Fatalf("up migration version %d is missing a corresponding .down.sql file", v)
		}
	}
}
