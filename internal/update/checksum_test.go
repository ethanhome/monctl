package update_test

import (
	"strings"
	"testing"

	"github.com/bacnh85/monctl/internal/update"
)

const (
	sumA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	sumB = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
)

func TestParseChecksums(t *testing.T) {
	in := sumA + "  monctl-darwin-arm64\r\n" +
		"\n" +
		sumB + " *monctl-windows-amd64.exe\n"

	got, err := update.ParseChecksums(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseChecksums: %v", err)
	}
	want := map[string]string{
		"monctl-darwin-arm64":      sumA,
		"monctl-windows-amd64.exe": sumB,
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d entries, want %d: %v", len(got), len(want), got)
	}
	for name, sum := range want {
		if got[name] != sum {
			t.Fatalf("%s = %q, want %q", name, got[name], sum)
		}
	}
}

func TestParseChecksumsRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"missing filename": sumA + "  ",
		"one field":        sumA + "\n",
		"short digest":     "abc  monctl-darwin-arm64\n",
		"non-hex digest":   strings.Repeat("z", 64) + "  monctl-darwin-arm64\n",
		"duplicate":        sumA + "  f\n" + sumB + "  f\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := update.ParseChecksums(strings.NewReader(in)); err == nil {
				t.Fatalf("ParseChecksums(%q) = nil error, want error", in)
			}
		})
	}
}
