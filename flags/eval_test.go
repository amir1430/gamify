package flags

import "testing"

// Frozen: these match production rollouts. Never change them.
func TestBucketVectors(t *testing.T) {
	for _, c := range []struct {
		key  string
		user int64
		want uint64
	}{
		{"new_lobby", 1, 8077},
		{"new_lobby", 2, 183},
		{"new_lobby", 42, 8072},
		{"shop_button", 1, 4657},
		{"crash.new_lobby", 1000000, 4810},
		{"a", 9223372036854775807, 6088},
	} {
		if got := bucket(c.key, c.user); got != c.want {
			t.Errorf("bucket(%q, %d) = %d, want %d", c.key, c.user, got, c.want)
		}
	}
}

func TestValue(t *testing.T) {
	testers := map[int64]struct{}{7: {}}
	// User 2's bucket for new_lobby is 183: in at 2%, out at 1%. User 1's is 8077.
	for name, c := range map[string]struct {
		e    entry
		user int64
		want bool
	}{
		"off":                 {entry{key: "new_lobby", percent: 100, testers: testers}, 1, false},
		"off beats tester":    {entry{key: "new_lobby", percent: 100, testers: testers}, 7, false},
		"on 0% tester":        {entry{key: "new_lobby", enabled: true, testers: testers}, 7, true},
		"on 0% other":         {entry{key: "new_lobby", enabled: true, testers: testers}, 2, false},
		"on 2% in bucket":     {entry{key: "new_lobby", enabled: true, percent: 2}, 2, true},
		"on 1% out of bucket": {entry{key: "new_lobby", enabled: true, percent: 1}, 2, false},
		"on 100%":             {entry{key: "new_lobby", enabled: true, percent: 100}, 1, true},
	} {
		if got := c.e.value(c.user); got != c.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
}

// Raising the percent only adds players, and each step holds about that share.
func TestRolloutOnlyAdds(t *testing.T) {
	prev := map[int64]bool{}
	for _, p := range []int{0, 1, 10, 50, 100} {
		e, n := entry{key: "new_lobby", enabled: true, percent: p}, 0
		for u := int64(1); u <= 20000; u++ {
			in := e.value(u)
			if prev[u] && !in {
				t.Fatalf("user %d dropped at %d%%", u, p)
			}
			prev[u] = in
			if in {
				n++
			}
		}
		if got := float64(n) / 200; got < float64(p)-1.5 || got > float64(p)+1.5 {
			t.Errorf("%d%%: %.2f%% of users", p, got)
		}
	}
}
