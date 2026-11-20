package update

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// ParseChecksums reads a SHA256SUMS document (the output of sha256sum) into
// a map from filename to lowercase hex digest. Blank lines are ignored; a
// leading '*' (binary-mode marker) is stripped. Malformed or duplicate
// entries are rejected rather than silently skipped.
func ParseChecksums(r io.Reader) (map[string]string, error) {
	sums := map[string]string{}
	sc := bufio.NewScanner(r)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) != 2 {
			return nil, fmt.Errorf("SHA256SUMS line %d: want <hash> <file>", line)
		}
		sum, name := strings.ToLower(fields[0]), strings.TrimPrefix(fields[1], "*")
		if len(sum) != 64 {
			return nil, fmt.Errorf("SHA256SUMS line %d: bad digest length", line)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return nil, fmt.Errorf("SHA256SUMS line %d: bad digest: %w", line, err)
		}
		if _, dup := sums[name]; dup {
			return nil, fmt.Errorf("SHA256SUMS line %d: duplicate entry for %q", line, name)
		}
		sums[name] = sum
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("SHA256SUMS: %w", err)
	}
	return sums, nil
}
