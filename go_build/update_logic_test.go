package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateWindowsExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "TokenMonitor.exe")
	data := make([]byte, 1024)
	copy(data[:2], "MZ")
	binary.LittleEndian.PutUint32(data[0x3c:0x40], 128)
	copy(data[128:132], "PE\x00\x00")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateWindowsExecutable(path); err != nil {
		t.Fatalf("valid PE rejected: %v", err)
	}
	data[0] = 'N'
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateWindowsExecutable(path); err == nil {
		t.Fatal("invalid PE accepted")
	}
}

func TestBuildWindowsUpdateScriptRetriesAndRestarts(t *testing.T) {
	script := buildWindowsUpdateScript(`C:\Program Files\TokenMonitor.exe`, `C:\Program Files\TokenMonitor.exe.new`, `C:\Program Files\version.txt`)
	for _, expected := range []string{"for /L %%I in (1,1,60)", "goto rollback", "del /Q \"%VERSION_FILE%\"", "start \"\" \"%CURRENT%\""} {
		if !strings.Contains(script, expected) {
			t.Fatalf("script missing %q", expected)
		}
	}
}
