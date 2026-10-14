package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestArchiveReclaimDirtyGraceConfig(t *testing.T) {
	t.Run("defaults and YAML provenance", func(t *testing.T) {
		cfg, err := LoadWithPath(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Tasks.ArchiveReclaim.DirtyGrace != 72*time.Hour {
			t.Fatalf("default dirty grace = %v, want 72h", cfg.Tasks.ArchiveReclaim.DirtyGrace)
		}

		configFile := filepath.Join(t.TempDir(), "config.yaml")
		contents := "tasks:\n  archiveReclaim:\n    dirtyGrace: 168h\n"
		if err := os.WriteFile(configFile, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err = LoadWithPath(configFile)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Tasks.ArchiveReclaim.DirtyGrace != 168*time.Hour {
			t.Fatalf("YAML dirty grace = %v, want 168h", cfg.Tasks.ArchiveReclaim.DirtyGrace)
		}
		if source := cfg.SourceFor("tasks.archiveReclaim.dirtyGrace"); source != SourceConfiguration {
			t.Errorf("source for tasks.archiveReclaim.dirtyGrace = %q, want configuration", source)
		}
	})

	t.Run("environment alias overrides YAML and zero disables", func(t *testing.T) {
		t.Setenv("KANDEV_TASK_ARCHIVE_RECLAIM_DIRTY_GRACE", "0")
		cfg, err := LoadWithPath(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Tasks.ArchiveReclaim.DirtyGrace != 0 {
			t.Fatalf("environment dirty grace = %v, want 0 (disabled)", cfg.Tasks.ArchiveReclaim.DirtyGrace)
		}
		if source := cfg.SourceFor("tasks.archiveReclaim.dirtyGrace"); source != SourceEnvironment {
			t.Errorf("source for tasks.archiveReclaim.dirtyGrace = %q, want environment", source)
		}
	})

	t.Run("negative values fail startup", func(t *testing.T) {
		t.Setenv("KANDEV_TASK_ARCHIVE_RECLAIM_DIRTY_GRACE", "-1h")
		if _, err := LoadWithPath(t.TempDir()); err == nil {
			t.Fatal("LoadWithPath accepted a negative dirty grace")
		}
	})

	t.Run("catalog entry is complete", func(t *testing.T) {
		entry, ok := CatalogEntryForKey("tasks.archiveReclaim.dirtyGrace")
		if !ok {
			t.Fatal("catalog is missing tasks.archiveReclaim.dirtyGrace")
		}
		if entry.Default != "72h" {
			t.Errorf("catalog default = %q, want 72h", entry.Default)
		}
		found := false
		for _, envVar := range entry.EnvVars {
			if envVar == "KANDEV_TASK_ARCHIVE_RECLAIM_DIRTY_GRACE" {
				found = true
			}
		}
		if !found {
			t.Errorf("catalog env aliases = %v, want KANDEV_TASK_ARCHIVE_RECLAIM_DIRTY_GRACE", entry.EnvVars)
		}
	})
}
