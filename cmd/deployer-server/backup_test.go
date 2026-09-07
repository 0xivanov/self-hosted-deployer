package main

import "testing"

func TestBackupCommandRequiresFlags(t *testing.T) {
	if got := backupCommand([]string{"create"}); got != 2 {
		t.Fatalf("backup create exit code = %d, want 2", got)
	}
	if got := backupCommand([]string{"restore"}); got != 2 {
		t.Fatalf("backup restore exit code = %d, want 2", got)
	}
}

func TestBackupCommandHelp(t *testing.T) {
	if got := runBackupCreate([]string{"-h"}); got != 0 {
		t.Fatalf("backup create help exit code = %d, want 0", got)
	}
	if got := runBackupRestore([]string{"-h"}); got != 0 {
		t.Fatalf("backup restore help exit code = %d, want 0", got)
	}
}
