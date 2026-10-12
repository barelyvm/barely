package serve

import "testing"

// TestDeletion pins which dockerd events schedule an fstrim: the ones that
// free space on the data disk, and not their neighbours (pull, die, create,
// a network's destroy). A missed one leaves freed space on the Mac until
// shutdown; a false one costs a needless trim.
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
