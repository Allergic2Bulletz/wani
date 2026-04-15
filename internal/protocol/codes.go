package protocol

import (
	"crypto/rand"
	_ "embed"
	"fmt"
	"math/big"
	"strings"
)

//go:embed wordlist.txt
var wordlistRaw string

// words is the parsed wordlist, loaded once at init time.
var words []string

// wordSet is a fast lookup set for ValidateCode.
var wordSet map[string]struct{}

func init() {
	lines := strings.Split(strings.TrimSpace(wordlistRaw), "\n")
	words = make([]string, 0, len(lines))
	wordSet = make(map[string]struct{}, len(lines))
	for _, line := range lines {
		w := strings.TrimSpace(line)
		if w != "" {
			words = append(words, w)
			wordSet[w] = struct{}{}
		}
	}
}

// GenerateCode produces an n-word pairing code using crypto/rand.
// Words are joined with hyphens, e.g. "ocean-river-tiger-lamp".
func GenerateCode(n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("protocol.GenerateCode: n must be positive")
	}
	selected := make([]string, n)
	max := big.NewInt(int64(len(words)))
	for i := range selected {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("protocol.GenerateCode: %w", err)
		}
		selected[i] = words[idx.Int64()]
	}
	return strings.Join(selected, "-"), nil
}

// ValidateCode reports whether code is a properly formatted pairing code whose
// words all appear in the wordlist.
func ValidateCode(code string) bool {
	parts := strings.Split(code, "-")
	if len(parts) == 0 {
		return false
	}
	for _, p := range parts {
		if _, ok := wordSet[p]; !ok {
			return false
		}
	}
	return true
}
