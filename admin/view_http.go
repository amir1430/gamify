package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	errViewFailed   = &apiError{http.StatusInternalServerError, "view_failed", "the view failed"}
	errActionFailed = &apiError{http.StatusInternalServerError, "action_failed", "the action failed, nothing was changed"}
)

func badFilter(msg string) *apiError { return &apiError{http.StatusBadRequest, "bad_filter", msg} }
func badInput(msg string) *apiError  { return &apiError{http.StatusBadRequest, "bad_input", msg} }

// registerViews adds the brand views' routes. They're open to any mfa session at the route; each
// handler checks the view's (and the action's) own permission.
func (adm *Admin) registerViews() {
	adm.handle("GET /api/views", "", adm.viewIndex)
	adm.handle("GET /api/views/{name}", "", adm.viewList)
	adm.handle("GET /api/views/{name}/{id}", "", adm.viewGet)
	adm.handle("POST /api/views/{name}/{id}/actions/{action}", "", adm.viewAction)
}

type actionDesc struct {
	Name    string  `json:"name"`
	Title   string  `json:"title"`
	Confirm string  `json:"confirm"`
	Reason  bool    `json:"reason"`
	Fields  []Field `json:"fields"`
}

type viewDesc struct {
	Name       string       `json:"name"`
	Title      string       `json:"title"`
	Group      string       `json:"group"`
	UserFilter string       `json:"user_filter"`
	Columns    []Column     `json:"columns"`
	Filters    []Filter     `json:"filters"`
	HasDetail  bool         `json:"has_detail"`
	Actions    []actionDesc `json:"actions"`
}

// viewIndex lists the views the role can see, in registration order, each with the actions the
// role can run.
func (adm *Admin) viewIndex(w http.ResponseWriter, r *http.Request, s *session) error {
	out := []viewDesc{}
	for _, v := range adm.views.all() {
		if !adm.can(s.Role, v.Perm) {
			continue
		}
		d := viewDesc{Name: v.Name, Title: v.Title, Group: v.Group, UserFilter: v.UserFilter,
			Columns: v.Columns, Filters: v.Filters, HasDetail: v.Get != nil, Actions: []actionDesc{}}
		if d.Filters == nil {
			d.Filters = []Filter{}
		}
		for _, a := range v.Actions {
			if adm.can(s.Role, a.Perm) {
				fields := a.Fields
				if fields == nil {
					fields = []Field{}
				}
				d.Actions = append(d.Actions, actionDesc{a.Name, a.Title, a.Confirm, a.Reason, fields})
			}
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out}) // no paging, so no "next" (as /api/games)
	return nil
}

// view is the {name} view, 404 if there's none, 403 if the role lacks its permission.
func (adm *Admin) view(r *http.Request, s *session) (*View, error) {
	v := adm.views.get(r.PathValue("name"))
	if v == nil {
		return nil, errNotFound
	}
	if !adm.can(s.Role, v.Perm) {
		return nil, errForbidden
	}
	return v, nil
}

func (adm *Admin) viewList(w http.ResponseWriter, r *http.Request, s *session) error {
	v, err := adm.view(r, s)
	if err != nil {
		return err
	}
	q, err := viewQuery(r, v)
	if err != nil {
		return err
	}
	p, err := guarded(r.Context(), v.Name, "list", errViewFailed, func() (Page, error) { return v.List(r.Context(), q) })
	if err != nil {
		return viewErr(r.Context(), v.Name, "list", err)
	}
	items := make([]Row, len(p.Items))
	for i, row := range p.Items {
		items[i] = encodeRow(v, row)
	}
	writeJSON(w, http.StatusOK, Page{Items: items, Next: p.Next})
	return nil
}

func (adm *Admin) viewGet(w http.ResponseWriter, r *http.Request, s *session) error {
	v, err := adm.view(r, s)
	if err != nil {
		return err
	}
	if v.Get == nil {
		return errNotFound
	}
	id, err := rowID(r)
	if err != nil {
		return err
	}
	row, err := guarded(r.Context(), v.Name, "get", errViewFailed, func() (Row, error) { return v.Get(r.Context(), id) })
	if err != nil {
		return viewErr(r.Context(), v.Name, "get", err)
	}
	if row == nil {
		return errNotFound
	}
	writeJSON(w, http.StatusOK, encodeRow(v, row))
	return nil
}

// viewAction runs an action and its audit row in one transaction.
func (adm *Admin) viewAction(w http.ResponseWriter, r *http.Request, s *session) error {
	v, err := adm.view(r, s)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(v.Actions, func(a Action) bool { return a.Name == r.PathValue("action") })
	if i < 0 {
		return errNotFound
	}
	a := v.Actions[i]
	if !adm.can(s.Role, a.Perm) {
		return errForbidden
	}
	id, err := rowID(r)
	if err != nil {
		return err
	}
	var b struct {
		Reason string                     `json:"reason"`
		Input  map[string]json.RawMessage `json:"input"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decode(w, r, &b); err != nil {
			return err
		}
	}
	reason := strings.TrimSpace(b.Reason)
	if a.Reason && reason == "" {
		return errReasonRequired
	}
	input, err := parseInput(a.Fields, b.Input)
	if err != nil {
		return err
	}

	ctx := r.Context()
	tx, err := adm.a.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	what := "action " + a.Name
	call := Call{Reason: reason, ID: id, Input: input, Admin: AdminRef{ID: s.AdminID, Email: s.Email, Role: s.Role}}
	ba, err := guarded(ctx, v.Name, what, errActionFailed, func() ([2]any, error) {
		before, after, err := a.Run(ctx, tx, call)
		return [2]any{before, after}, err
	})
	if err != nil {
		var e *apiError
		if errors.Is(err, ErrNotFound) {
			return errNotFound
		}
		if !errors.As(err, &e) {
			slog.ErrorContext(ctx, "admin: view action failed", "view", v.Name, "action", a.Name, "id", id, "err", err)
			err = errActionFailed
		}
		return err
	}
	if err := adm.audit(ctx, tx, s, r, "view."+v.Name+"."+a.Name, v.Name+":"+strconv.FormatInt(id, 10), ba[0], ba[1], reason); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if a.After != nil {
		_, _ = guarded(ctx, v.Name, what+" after", errActionFailed, func() (struct{}, error) {
			a.After(context.WithoutCancel(ctx), call, ba[0], ba[1])
			return struct{}{}, nil
		})
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// guarded runs f, turning a panic into failed. The panic is logged with its stack, never with
// the row or the inputs.
func guarded[T any](ctx context.Context, view, what string, failed *apiError, f func() (T, error)) (out T, err error) {
	defer func() {
		if p := recover(); p != nil {
			slog.ErrorContext(ctx, "admin: view panicked", "view", view, "in", what, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			err = failed
		}
	}()
	return f()
}

// viewErr maps an error from a view's List or Get: an *apiError passes, ErrNotFound is 404,
// anything else is logged and view_failed.
func viewErr(ctx context.Context, view, what string, err error) error {
	var e *apiError
	switch {
	case errors.As(err, &e):
		return e
	case errors.Is(err, ErrNotFound):
		return errNotFound
	}
	slog.ErrorContext(ctx, "admin: view failed", "view", view, "in", what, "err", err)
	return errViewFailed
}

// rowID reads {id}: a positive integer.
func rowID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, badRequest("id must be a positive integer")
	}
	return id, nil
}

// viewQuery reads the list's filters, before and limit. An empty filter value is no filter.
func viewQuery(r *http.Request, v *View) (Query, error) {
	before, limit, err := pageParams(r)
	if err != nil {
		return Query{}, err
	}
	q := Query{Filters: map[string]string{}, Before: before, Limit: limit}
	for key, vals := range r.URL.Query() {
		if key == "before" || key == "limit" {
			continue
		}
		i := slices.IndexFunc(v.Filters, func(f Filter) bool { return f.Key == key })
		if i < 0 {
			return Query{}, badFilter("unknown filter " + strconv.Quote(key))
		}
		if len(vals) != 1 {
			return Query{}, badFilter(key + " is given more than once")
		}
		if vals[0] == "" {
			continue
		}
		if err := checkFilter(v.Filters[i], vals[0]); err != nil {
			return Query{}, err
		}
		q.Filters[key] = vals[0]
	}
	return q, nil
}

func checkFilter(f Filter, s string) error {
	ok := true
	switch f.Type {
	case Int, UserID, Money, Decimal:
		_, err := strconv.ParseInt(s, 10, 64)
		ok = err == nil
	case Bool:
		ok = s == "true" || s == "false"
	case Enum:
		ok = slices.Contains(f.Options, s)
	case Time:
		_, err := time.Parse(time.RFC3339, s)
		ok = err == nil
	}
	if !ok {
		return badFilter(fmt.Sprintf("%s: not a valid %s", f.Key, f.Type))
	}
	return nil
}

// parseInput checks an action's inputs against its fields. See Call for the Go types.
func parseInput(fields []Field, raw map[string]json.RawMessage) (map[string]any, error) {
	for k := range raw {
		if !slices.ContainsFunc(fields, func(f Field) bool { return f.Key == k }) {
			return nil, badInput("unknown input " + strconv.Quote(k))
		}
	}
	out := map[string]any{}
	for _, f := range fields {
		b, ok := raw[f.Key]
		if !ok || bytes.Equal(bytes.TrimSpace(b), []byte("null")) || string(b) == `""` {
			if f.Required {
				return nil, badInput(f.Key + " is required")
			}
			continue
		}
		v, ok := parseField(f, b)
		if !ok {
			return nil, badInput(fmt.Sprintf("%s: not a valid %s", f.Key, f.Type))
		}
		out[f.Key] = v
	}
	return out, nil
}

func parseField(f Field, b json.RawMessage) (any, bool) {
	var s string
	switch f.Type {
	case Int, UserID, Money, Decimal:
		// A JSON integer, or a numeric string for values beyond 2^53.
		lit := string(b)
		if json.Unmarshal(b, &s) == nil {
			lit = s
		}
		n, err := strconv.ParseInt(lit, 10, 64)
		return n, err == nil
	case Bool:
		var v bool
		return v, json.Unmarshal(b, &v) == nil
	case JSON:
		return json.RawMessage(bytes.Clone(b)), true
	}
	if json.Unmarshal(b, &s) != nil {
		return nil, false
	}
	switch f.Type {
	case Enum:
		return s, slices.Contains(f.Options, s)
	case Time:
		t, err := time.Parse(time.RFC3339, s)
		return t, err == nil
	case URL, Image:
		return s, webURL(s)
	}
	return s, true // Text
}

// webURL reports whether s is an absolute http(s) URL (never javascript: or data:, which the
// console would put in an href or src).
func webURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// maxSafeInt is 2^53: beyond it a JSON number loses precision in the browser.
const maxSafeInt = 1 << 53

// encodeRow is row with the integers of integer columns beyond ±2^53 as strings.
func encodeRow(v *View, row Row) Row {
	out := make(Row, len(row))
	for k, x := range row {
		out[k] = x
		i := slices.IndexFunc(v.Columns, func(c Column) bool { return c.Key == k })
		if i < 0 || !v.Columns[i].Type.integer() {
			continue
		}
		switch n := x.(type) {
		case int:
			if n > maxSafeInt || n < -maxSafeInt {
				out[k] = strconv.Itoa(n)
			}
		case int64:
			if n > maxSafeInt || n < -maxSafeInt {
				out[k] = strconv.FormatInt(n, 10)
			}
		case uint64:
			if n > maxSafeInt {
				out[k] = strconv.FormatUint(n, 10)
			}
		}
	}
	return out
}
