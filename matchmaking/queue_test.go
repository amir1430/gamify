package matchmaking

import (
	"testing"
	"time"

	"github.com/amir1430/gamify/gameconfig"
	"github.com/amir1430/gamify/ledger"
)

func TestQueueValidation(t *testing.T) {
	g := &gameconfig.Game{Name: "arena"}
	stakes := map[ledger.Currency][]int64{"coin": {100}}
	one := []Team{{1, 1}}
	bad := map[string]QueueConfig{
		"empty name":        {Teams: one},
		"no teams":          {Name: "q"},
		"min>max":           {Name: "q", Teams: []Team{{2, 1}}},
		"min<1":             {Name: "q", Teams: []Team{{0, 1}}},
		"ffa teams":         {Name: "q", Teams: []Team{{1, 2}}, FFA: &Team{2, 8}},
		"ffa min<2":         {Name: "q", Teams: one, FFA: &Team{1, 8}},
		"stakes no game":    {Name: "q", Teams: one, Stakes: stakes},
		"stake<=0":          {Name: "q", Teams: one, Game: g, Stakes: map[ledger.Currency][]int64{"coin": {0}}},
		"fee>10000":         {Name: "q", Teams: one, FeeBP: 10_001},
		"duplicate (first)": {Name: "dup", Teams: one},
	}
	s := &Service{}
	if _, err := s.Queue(QueueConfig{Name: "dup", Teams: one}); err != nil {
		t.Fatal(err)
	}
	for name, c := range bad {
		if name == "duplicate (first)" {
			name = "duplicate"
		}
		if _, err := s.Queue(c); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	for name, c := range map[string]QueueConfig{
		"1v1": {Name: "a", Teams: one}, "5v5": {Name: "b", Teams: []Team{{5, 5}, {5, 5}}},
		"1v4": {Name: "c", Teams: []Team{{1, 1}, {4, 4}}}, "ffa": {Name: "d", Teams: one, FFA: &Team{2, 8}},
	} {
		q, err := s.Queue(c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cfg := q.Config()
		if cfg.FillAfter != 10*time.Second || cfg.MaxWait != 2*time.Minute || cfg.MatchTimeout != time.Hour ||
			cfg.RatingKey != c.Name || cfg.FeeBP != ledger.FeeBP {
			t.Errorf("%s: defaults %+v", name, cfg)
		}
	}
	q, err := s.Queue(QueueConfig{Name: "free", Teams: one, NoFee: true, MaxWait: Forever})
	if err != nil || q.Config().FeeBP != 0 || q.Config().MaxWait != Forever {
		t.Fatalf("NoFee/Forever: %+v %v", q, err)
	}
}
