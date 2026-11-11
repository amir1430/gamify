package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"sync"
)

// AlertTone is how loudly the console shows a non-zero alert.
type AlertTone string

const (
	AlertWarn   AlertTone = "warn"   // work waiting for someone (the default)
	AlertDanger AlertTone = "danger" // something stuck or failing
)

// Alert is a count of things an operator should look at (withdrawals to approve, stuck payouts).
// The dashboard lists the ones that aren't zero; with View, the count links to that view filtered
// by Filters and badges the view in the menu. Count runs on every dashboard refresh, so it must
// be quick (an indexed count).
type Alert struct {
	Name, Title string
	Hint        string    // a quieter line under the title
	Perm        Perm      // who sees it: built-in or Config.Perms
	Tone        AlertTone // "" = AlertWarn
	View        string    // a registered view, or "" for no link
	Filters     map[string]string
	Count       func(ctx context.Context) (int64, error)
}

// Status is a table on the system page: a module's own setup and health (its workers, the
// methods it registered). Rows runs on every load of the page.
type Status struct {
	Name, Title string
	Perm        Perm
	Columns     []Column
	Rows        func(ctx context.Context) ([]Row, error)
}

// extras holds the registered alerts and status sections, in registration order.
type extras struct {
	mu     sync.RWMutex
	alerts []*Alert
	status []*Status
}

// Alert adds an alert. Call it after the View it names and before App.Run. It returns an error for
// a bad or duplicate name, no title, an unknown permission or tone, no Count, an unknown View,
// Filters without a View, or a filter the view doesn't have or a value it would refuse.
func (adm *Admin) Alert(a Alert) error {
	if err := adm.checkAlert(&a); err != nil {
		return fmt.Errorf("admin: alert %q: %w", a.Name, err)
	}
	ex := &adm.extras
	ex.mu.Lock()
	defer ex.mu.Unlock()
	if slices.ContainsFunc(ex.alerts, func(x *Alert) bool { return x.Name == a.Name }) {
		return fmt.Errorf("admin: alert %q: name already registered", a.Name)
	}
	ex.alerts = append(ex.alerts, &a)
	return nil
}

func (adm *Admin) checkAlert(a *Alert) error {
	switch {
	case !viewName.MatchString(a.Name):
		return errors.New("name must match " + viewName.String())
	case a.Title == "":
		return errors.New("title is required")
	case !adm.knownPerm(a.Perm):
		return fmt.Errorf("unknown perm %q", a.Perm)
	case a.Count == nil:
		return errors.New("count is required")
	case a.Tone != "" && a.Tone != AlertWarn && a.Tone != AlertDanger:
		return fmt.Errorf("unknown tone %q", a.Tone)
	case a.View == "" && len(a.Filters) > 0:
		return errors.New("filters need a view")
	}
	if a.Tone == "" {
		a.Tone = AlertWarn
	}
	a.Filters = maps.Clone(a.Filters)
	if a.View == "" {
		return nil
	}
	v := adm.views.get(a.View)
	if v == nil {
		return fmt.Errorf("unknown view %q (register it first)", a.View)
	}
	for k, val := range a.Filters {
		i := slices.IndexFunc(v.Filters, func(f Filter) bool { return f.Key == k })
		if i < 0 {
			return fmt.Errorf("filter %q: view %q has none", k, a.View)
		}
		if err := checkFilter(v.Filters[i], val); err != nil {
			return fmt.Errorf("filter %q: %w", k, err)
		}
	}
	return nil
}

// Status adds a section to the system page. Call it before App.Run. It returns an error for a bad
// or duplicate name, no title, an unknown permission, no Rows, or columns View would refuse.
func (adm *Admin) Status(s Status) error {
	err := func() error {
		switch {
		case !viewName.MatchString(s.Name):
			return errors.New("name must match " + viewName.String())
		case s.Title == "":
			return errors.New("title is required")
		case !adm.knownPerm(s.Perm):
			return fmt.Errorf("unknown perm %q", s.Perm)
		case s.Rows == nil:
			return errors.New("rows is required")
		}
		var err error
		s.Columns, _, err = checkColumns(s.Columns)
		return err
	}()
	if err != nil {
		return fmt.Errorf("admin: status %q: %w", s.Name, err)
	}
	ex := &adm.extras
	ex.mu.Lock()
	defer ex.mu.Unlock()
	if slices.ContainsFunc(ex.status, func(x *Status) bool { return x.Name == s.Name }) {
		return fmt.Errorf("admin: status %q: name already registered", s.Name)
	}
	ex.status = append(ex.status, &s)
	return nil
}

type alertOut struct {
	Name    string            `json:"name"`
	Title   string            `json:"title"`
	Hint    string            `json:"hint"`
	Tone    AlertTone         `json:"tone"`
	View    string            `json:"view"`
	Filters map[string]string `json:"filters"`
	Count   *int64            `json:"count"`
	Error   *string           `json:"error"`
}

// alerts lists the alerts the role may see with their counts. A failing Count is logged and shown
// as that alert's error, so one broken module never hides the others.
func (adm *Admin) alerts(w http.ResponseWriter, r *http.Request, s *session) error {
	adm.extras.mu.RLock()
	all := slices.Clone(adm.extras.alerts)
	adm.extras.mu.RUnlock()
	out := []alertOut{}
	for _, a := range all {
		if !adm.can(s.Role, a.Perm) {
			continue
		}
		o := alertOut{Name: a.Name, Title: a.Title, Hint: a.Hint, Tone: a.Tone, View: a.View, Filters: a.Filters}
		if o.Filters == nil {
			o.Filters = map[string]string{}
		}
		n, err := guarded(r.Context(), a.Name, "alert", errViewFailed, func() (int64, error) { return a.Count(r.Context()) })
		if err != nil {
			slog.ErrorContext(r.Context(), "admin: alert failed", "alert", a.Name, "err", err)
			msg := "unavailable"
			o.Error = &msg
		} else {
			o.Count = &n
		}
		out = append(out, o)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
	return nil
}

type statusOut struct {
	Name    string   `json:"name"`
	Title   string   `json:"title"`
	Columns []Column `json:"columns"`
	Items   []Row    `json:"items"`
	Error   *string  `json:"error"`
}

// statusSections runs the role's status sections for the system page.
func (adm *Admin) statusSections(ctx context.Context, role Role) []statusOut {
	adm.extras.mu.RLock()
	all := slices.Clone(adm.extras.status)
	adm.extras.mu.RUnlock()
	out := []statusOut{}
	for _, st := range all {
		if !adm.can(role, st.Perm) {
			continue
		}
		o := statusOut{Name: st.Name, Title: st.Title, Columns: st.Columns, Items: []Row{}}
		rows, err := guarded(ctx, st.Name, "status", errViewFailed, func() ([]Row, error) { return st.Rows(ctx) })
		if err != nil {
			slog.ErrorContext(ctx, "admin: status failed", "status", st.Name, "err", err)
			msg := "unavailable"
			o.Error = &msg
		} else if rows != nil {
			o.Items = rows
		}
		out = append(out, o)
	}
	return out
}
