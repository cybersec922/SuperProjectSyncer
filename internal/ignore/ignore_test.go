package ignore

import "testing"

func TestMatcher(t *testing.T) {
	m := New([]string{".env", "*.log", "node_modules/**", "secrets/", ".git/**"})

	cases := []struct {
		path string
		want bool
	}{
		{".env", true},
		{"src/.env", true},
		{"app/main.go", false},
		{"debug.log", true},
		{"node_modules/pkg/index.js", true},
		{"src/node_modules/x", true},
		{"secrets/key.pem", true},
		{".git/config", true},
		{"readme.md", false},
	}
	for _, c := range cases {
		got := m.Ignored(c.path)
		if got != c.want {
			t.Errorf("Ignored(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
