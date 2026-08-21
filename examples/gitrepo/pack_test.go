package gitrepo

import (
	"strings"
	"testing"
)

func TestApplyDelta(t *testing.T) {
	base := []byte("hello world")

	tests := []struct {
		name  string
		delta []byte
		want  string
	}{
		{
			// Copy "hello " from the base, then insert a literal.
			name:  "copy and insert",
			delta: []byte{11, 11, 0x90, 0x06, 0x05, 't', 'h', 'e', 'r', 'e'},
			want:  "hello there",
		},
		{
			// A copy with an explicit offset takes "world".
			name:  "copy with offset",
			delta: []byte{11, 5, 0x91, 0x06, 0x05},
			want:  "world",
		},
		{
			name:  "literal only",
			delta: []byte{11, 3, 0x03, 'n', 'e', 'w'},
			want:  "new",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := applyDelta(base, tt.delta)
			if err != nil {
				t.Fatalf("applyDelta: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("applyDelta = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApplyDeltaRejectsBadInput(t *testing.T) {
	base := []byte("hello world")

	tests := []struct {
		name  string
		base  []byte
		delta []byte
		want  string
	}{
		{"base size mismatch", base, []byte{5, 5, 0x03, 'a', 'b', 'c'}, "base"},
		{"copy past the end", base, []byte{11, 99, 0x91, 0x40, 0x40}, "past the end"},
		{"reserved instruction", base, []byte{11, 1, 0x00}, "reserved"},
		{"truncated literal", base, []byte{11, 9, 0x09, 'a'}, "truncated"},
		{"short output", base, []byte{11, 99, 0x03, 'a', 'b', 'c'}, "want 99"},
		{"truncated size", base, []byte{0x80}, "truncated delta size"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := applyDelta(tt.base, tt.delta)
			if err == nil {
				t.Fatal("applyDelta succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestTypeName(t *testing.T) {
	for typ, want := range map[int]string{
		objCommit: "commit",
		objTree:   "tree",
		objBlob:   "blob",
		objTag:    "tag",
		9:         "unknown",
	} {
		if got := typeName(typ); got != want {
			t.Errorf("typeName(%d) = %q, want %q", typ, got, want)
		}
	}
}
