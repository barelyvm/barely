package serve

import "testing"

func TestDeletion(t *testing.T) {
	cases := []struct {
		typ, action string
		want        bool
	}{
		{"image", "delete", true},
		{"image", "prune", true},
		{"image", "pull", false},
		{"container", "destroy", true},
		{"container", "die", false},
		{"volume", "destroy", true},
		{"volume", "create", false},
		{"builder", "prune", true},
		{"network", "destroy", false},
	}
	for _, c := range cases {
		if got := deletion(c.typ, c.action); got != c.want {
			t.Errorf("deletion(%q, %q) = %v, want %v", c.typ, c.action, got, c.want)
		}
	}
}
