package filer

import (
	"testing"
)

func TestFullPathDirAndName(t *testing.T) {
	tests := []struct {
		input   FullPath
		wantDir FullPath
		wantName string
	}{
		{"/photos/vacation.jpg", "/photos", "vacation.jpg"},
		{"/file.txt", "/", "file.txt"},
		{"/", "/", ""},
	}

	for _, tt := range tests {
		dir, name := tt.input.DirAndName()
		if dir != tt.wantDir {
			t.Errorf("DirAndName(%q): dir = %q, want %q", tt.input, dir, tt.wantDir)
		}
		if name != tt.wantName {
			t.Errorf("DirAndName(%q): name = %q, want %q", tt.input, name, tt.wantName)
		}
	}
}
