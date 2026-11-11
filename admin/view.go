package admin

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"sync"

	"github.com/amir1430/gamify/ledger"

	"github.com/jackc/pgx/v5"
)

// FieldType is how the console shows a column and parses a filter or an action's input.
type FieldType string

const (
	Text    FieldType = "text"
	Int     FieldType = "int"
	Decimal FieldType = "decimal" // an integer shown with Scale decimals (237, scale 2: 2.37), never a float
	Money   FieldType = "money"   // minor units of the row's CurrencyKey or a fixed Currency
	Bool    FieldType = "bool"
	Time    FieldType = "time"
	Enum    FieldType = "enum" // one of Options
	UserID  FieldType = "user_id"
	JSON    FieldType = "json"
	URL     FieldType = "url"     // an http(s) link, opened in a new tab; an input must be one
	Image   FieldType = "image"   // an http(s) image URL: a thumbnail in the table, larger in the detail
	Strings FieldType = "strings" // a list of strings: with Options, any of them; without, free tags
)

var fieldTypes = []FieldType{Text, Int, Decimal, Money, Bool, Time, Enum, UserID, JSON, URL, Image, Strings}

// integer reports whether values of t are integers (sent as JSON strings beyond ±2^53).
func (t FieldType) integer() bool { return t == Int || t == Money || t == UserID || t == Decimal }

// Column is one column of a view's table and detail page. Label "" = Key.
type Column struct {
	Key         string          `json:"key"`
	Label       string          `json:"label"`
	Type        FieldType       `json:"type"`
	Scale       uint8           `json:"scale,omitempty"`        // Decimal: 1..18
	CurrencyKey string          `json:"currency_key,omitempty"` // Money: the row key holding the currency...
	Currency    ledger.Currency `json:"currency,omitempty"`     // ...or a fixed currency (one of the two)
	Options     []string        `json:"options,omitempty"`      // Enum
	DetailOnly  bool            `json:"detail_only,omitempty"`  // shown in the row's detail, not as a table column
	// OptionLabels are the names shown for Enum and Strings values, by value (default: the value).
	OptionLabels map[string]string `json:"option_labels,omitempty"`
}

// Filter is a query parameter of a view's list. Key is a column's, with the same Type; an Enum
// filter without Options uses the column's.
type Filter struct {
	Key          string            `json:"key"`
	Label        string            `json:"label"`
	Type         FieldType         `json:"type"`
	Options      []string          `json:"options,omitempty"`
	OptionLabels map[string]string `json:"option_labels,omitempty"` // default: the column's
}

// Option is one choice of an Enum field with OptionsFrom: the row's value at that key is a list
// of these. Label "" = Value. Hint is a second, quieter line. Disabled shows it but can't be picked.
type Option struct {
	Value    string `json:"value"`
	Label    string `json:"label,omitempty"`
	Hint     string `json:"hint,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

// Field is an input of an action. Money and Decimal inputs are integers (minor units, scaled).
// The form prefills it from the row's value at the same key.
type Field struct {
	Key      string          `json:"key"`
	Label    string          `json:"label"`
	Type     FieldType       `json:"type"`
	Required bool            `json:"required"`
	Scale    uint8           `json:"scale,omitempty"`
	Currency ledger.Currency `json:"currency,omitempty"`
	Options  []string        `json:"options,omitempty"`

	Help        string `json:"help,omitempty"`        // a line under the input: what it does, what empty means
	Placeholder string `json:"placeholder,omitempty"` // what an empty text input shows
	Group       string `json:"group,omitempty"`       // a section heading; fields in a row with the same Group sit under it
	// OptionHelp is a line next to an option, by the option's value (Enum and Strings). Those
	// options are listed first.
	OptionHelp map[string]string `json:"option_help,omitempty"`
	// OptionLabels are the names shown for options, by value (default: the value).
	OptionLabels map[string]string `json:"option_labels,omitempty"`
	// OptionsFrom makes an Enum's choices depend on the row: the row key (a column or not, from
	// List or Get) holding them as []Option. It replaces Options, and the console can't check the
	// value against a row, so the action must.
	OptionsFrom string `json:"options_from,omitempty"`
}

// Row is one record of a view, keyed by column key, encoded with encoding/json. An int, int64 or
// uint64 in an Int, Money, UserID or Decimal column beyond ±2^53 is sent as a JSON string.
type Row = map[string]any

// Page is one page of a view's list: Next is the Before of the next page, nil on the last.
type Page struct {
	Items []Row  `json:"items"`
	Next  *int64 `json:"next"`
}

// Query is a list request. Filters hold the request's filter values, already checked against
// their types (Int/UserID/Money/Decimal: an int64, Bool: "true"/"false", Enum: one of the
// options, Time: RFC 3339); a filter that isn't set is absent. Before is 0 on the first page;
// Limit is 1..100.
type Query struct {
	Filters map[string]string
	Before  int64
	Limit   int32
}

// Call is one run of an action. Input holds the validated inputs by field key: int64 for Int,
// Money, Decimal and UserID; bool for Bool; string for Text and Enum; time.Time for Time;
// json.RawMessage for JSON. An optional input that wasn't sent is absent.
type Call struct {
	Reason string // the trimmed reason, "" unless Action.Reason
	ID     int64
	Input  map[string]any
	Admin  AdminRef
}

// AdminRef is the console user running an action.
type AdminRef struct {
	ID    int64
	Email string
	Role  Role
}

// ActionState is what the console shows about an action on one row. The zero value is a plain
// available action.
type ActionState struct {
	Unavailable string `json:"unavailable,omitempty"` // why it can't run on this row: shown greyed out with this reason
	Impact      string `json:"impact,omitempty"`      // what it will do to this row, shown as a warning in its form
}

// Action is a button on a view's row (View.Actions) or on the page (View.PageActions). Run runs inside the admin's transaction: the console
// writes the audit row (action view.<view>.<action>, target <view>:<id>, Run's before and
// after, the reason) in tx and commits. An error or a panic rolls everything back. Run never
// moves money: the ledger isn't in tx. Change the game's row and let its reconciler do the rest.
type Action struct {
	Name, Title string
	Confirm     string // "" = no confirmation prompt
	Perm        Perm   // built-in or Config.Perms; the view's Perm is needed too
	Reason      bool   // a reason is required
	Fields      []Field
	// TypeConfirm, if set, is text the operator must type in the form before it can be sent, for
	// what's hard to undo.
	TypeConfirm string
	// State, if set, says how this action stands on a row, from the row's values. It runs for every
	// row listed, so it must be quick, and it's only what the console shows: Run still refuses a row
	// that can't take the action. Row actions only.
	State func(Row) ActionState
	// TargetField, on a page action, names a required Int or UserID field holding the row it acts
	// on: that's Call.ID, and the audit row's target. Without it, ID is 0 and the target is the view.
	TargetField string
	Run         func(ctx context.Context, tx pgx.Tx, c Call) (before, after any, err error)
	// After, if set, runs once the transaction has committed, with what Run returned (for pushes
	// and hooks that must not fire for a rolled-back action). It can't fail the action: log in it.
	After func(ctx context.Context, c Call, before, after any)
}

// View is a page the brand describes in Go and the console draws: a filtered, paged table, an
// optional detail page and actions. UserFilter names an Int or UserID filter: the view is also
// a tab on the player page, filtered to that player.
type View struct {
	Name, Title, Group string
	Perm               Perm // built-in or Config.Perms
	UserFilter         string
	Columns            []Column
	Filters            []Filter
	List               func(ctx context.Context, q Query) (Page, error)
	Get                func(ctx context.Context, id int64) (Row, error) // nil = no detail page; ErrNotFound = 404
	Actions            []Action
	Summary            []string // column keys shown atop an action's form, so it's clear which row it changes
	PageActions        []Action // buttons on the page, not on a row (a new record, say): no row, no prefill
}

// ErrNotFound is what a view's Get returns for a missing row (404).
var ErrNotFound = errors.New("admin: not found")

// BadInput is what an action returns for input the operator got wrong: 400, not logged as a failure.
func BadInput(msg string) error { return badRequest(msg) }

// Conflict is what an action returns for a row that can't take the action now (already closed,
// a different result): 409, not logged as a failure.
func Conflict(msg string) error { return &apiError{http.StatusConflict, "conflict", msg} }

var viewName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// views is the registry: by name, plus the registration order for the menu.
type views struct {
	mu     sync.RWMutex
	byName map[string]*View
	order  []*View
}

func (vs *views) get(name string) *View {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.byName[name]
}

func (vs *views) all() []*View {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return slices.Clone(vs.order)
}

// View adds a brand view. Call it after Register and before App.Run. It returns an error for an
// invalid view: a bad or duplicate name, no title, an unknown permission, no columns, a duplicate
// column, a Money column without exactly one of CurrencyKey and Currency, a Decimal with Scale
// outside 1..18, an Enum without options, a filter that isn't a column of the same type, a
// UserFilter that isn't an Int or UserID filter, a nil List, or an action with a bad or duplicate
// name, no Run or an unknown permission, a Strings filter, an OptionsFrom on anything but an Enum
// or with Options, an OptionHelp or OptionLabels for an unknown option, a Summary key that isn't a
// column, an action name used twice across Actions and PageActions, or a TargetField that isn't a
// required Int or UserID field of a page action. State needs a row, so a page action can't have it.
func (adm *Admin) View(v View) error {
	v, err := adm.checkView(v)
	if err != nil {
		return fmt.Errorf("admin: view %q: %w", v.Name, err)
	}
	vs := &adm.views
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if _, ok := vs.byName[v.Name]; ok {
		return fmt.Errorf("admin: view %q: name already registered", v.Name)
	}
	if vs.byName == nil {
		vs.byName = map[string]*View{}
	}
	vs.byName[v.Name] = &v
	vs.order = append(vs.order, &v)
	return nil
}

// checkView validates v and returns a copy the caller can't change afterwards.
func (adm *Admin) checkView(v View) (View, error) {
	if !viewName.MatchString(v.Name) {
		return v, errors.New("name must match " + viewName.String())
	}
	if v.Title == "" {
		return v, errors.New("title is required")
	}
	if !adm.knownPerm(v.Perm) {
		return v, fmt.Errorf("unknown perm %q", v.Perm)
	}
	if len(v.Columns) == 0 {
		return v, errors.New("columns: at least one is required")
	}
	if v.List == nil {
		return v, errors.New("list is required")
	}
	var cols map[string]Column
	var err error
	if v.Columns, cols, err = checkColumns(v.Columns); err != nil {
		return v, err
	}
	filters := map[string]Filter{}
	v.Filters = slices.Clone(v.Filters)
	for i, f := range v.Filters {
		c, ok := cols[f.Key]
		switch {
		case !ok:
			return v, fmt.Errorf("filter %q: not a column", f.Key)
		case f.Type != c.Type:
			return v, fmt.Errorf("filter %q: type %q, the column's is %q", f.Key, f.Type, c.Type)
		case f.Type == JSON || f.Type == Image || f.Type == Strings:
			return v, fmt.Errorf("filter %q: %s columns can't be filtered", f.Key, f.Type)
		case f.Key == "before" || f.Key == "limit":
			return v, fmt.Errorf("filter %q: the name is the list's paging parameter", f.Key)
		case filters[f.Key].Key != "":
			return v, fmt.Errorf("filter %q: duplicate key", f.Key)
		}
		if len(f.Options) == 0 {
			f.Options = c.Options
		}
		if len(f.OptionLabels) == 0 {
			f.OptionLabels = c.OptionLabels
		}
		if f.Type == Enum && len(f.Options) == 0 {
			return v, fmt.Errorf("filter %q: enum needs options", f.Key)
		}
		if f.Label == "" {
			f.Label = c.Label
		}
		f.Options, f.OptionLabels = slices.Clone(f.Options), maps.Clone(f.OptionLabels)
		v.Filters[i], filters[f.Key] = f, f
	}
	if v.UserFilter != "" {
		if f, ok := filters[v.UserFilter]; !ok || (f.Type != Int && f.Type != UserID) {
			return v, fmt.Errorf("user_filter %q: not an int or user_id filter", v.UserFilter)
		}
	}
	v.Summary = slices.Clone(v.Summary)
	for _, k := range v.Summary {
		if _, ok := cols[k]; !ok {
			return v, fmt.Errorf("summary %q: not a column", k)
		}
	}
	v.Actions, v.PageActions = slices.Clone(v.Actions), slices.Clone(v.PageActions)
	seen := map[string]bool{}
	for _, l := range []struct {
		actions []Action
		page    bool
	}{{v.Actions, false}, {v.PageActions, true}} {
		for i, a := range l.actions {
			if seen[a.Name] {
				return v, fmt.Errorf("action %q: duplicate name", a.Name)
			}
			seen[a.Name] = true
			a, err := adm.checkAction(a, l.page)
			if err != nil {
				return v, fmt.Errorf("action %q: %w", a.Name, err)
			}
			l.actions[i] = a
		}
	}
	return v, nil
}

// checkColumns validates cols and returns a copy with labels defaulted, and the columns by key.
func checkColumns(in []Column) ([]Column, map[string]Column, error) {
	if len(in) == 0 {
		return nil, nil, errors.New("columns: at least one is required")
	}
	cols := map[string]Column{}
	out := slices.Clone(in)
	for i, c := range out {
		if c.Key == "" {
			return nil, nil, fmt.Errorf("column %d: key is required", i)
		}
		if _, ok := cols[c.Key]; ok {
			return nil, nil, fmt.Errorf("column %q: duplicate key", c.Key)
		}
		if err := checkType(c.Type, c.Scale, c.Options); err != nil {
			return nil, nil, fmt.Errorf("column %q: %w", c.Key, err)
		}
		if c.Type == Money && (c.CurrencyKey == "") == (c.Currency == "") {
			return nil, nil, fmt.Errorf("column %q: money needs one of CurrencyKey and Currency", c.Key)
		}
		if c.Label == "" {
			c.Label = c.Key
		}
		c.Options, c.OptionLabels = slices.Clone(c.Options), maps.Clone(c.OptionLabels)
		out[i], cols[c.Key] = c, c
	}
	return out, cols, nil
}

// checkAction validates a and returns a copy with its fields' defaults set.
func (adm *Admin) checkAction(a Action, page bool) (Action, error) {
	switch {
	case !viewName.MatchString(a.Name):
		return a, fmt.Errorf("name must match %s", viewName)
	case a.Title == "":
		return a, errors.New("title is required")
	case a.Run == nil:
		return a, errors.New("run is required")
	case !adm.knownPerm(a.Perm):
		return a, fmt.Errorf("unknown perm %q", a.Perm)
	case page && a.State != nil:
		return a, errors.New("state needs a row, a page action has none")
	case !page && a.TargetField != "":
		return a, errors.New("target_field is for page actions")
	}
	a.Fields = slices.Clone(a.Fields)
	keys := map[string]bool{}
	for j, f := range a.Fields {
		if f.Key == "" || keys[f.Key] {
			return a, fmt.Errorf("field %q: key empty or duplicate", f.Key)
		}
		keys[f.Key] = true
		if err := checkField(f); err != nil {
			return a, fmt.Errorf("field %q: %w", f.Key, err)
		}
		if f.Label == "" {
			f.Label = f.Key
		}
		f.Options, f.OptionHelp, f.OptionLabels = slices.Clone(f.Options), maps.Clone(f.OptionHelp), maps.Clone(f.OptionLabels)
		a.Fields[j] = f
	}
	if a.TargetField != "" {
		i := slices.IndexFunc(a.Fields, func(f Field) bool { return f.Key == a.TargetField })
		if i < 0 || !a.Fields[i].Required || (a.Fields[i].Type != Int && a.Fields[i].Type != UserID) {
			return a, fmt.Errorf("target_field %q: not a required int or user_id field", a.TargetField)
		}
	}
	return a, nil
}

func checkField(f Field) error {
	if f.OptionsFrom != "" {
		switch {
		case f.Type != Enum:
			return errors.New("options_from needs an enum")
		case len(f.Options) > 0:
			return errors.New("options_from replaces options")
		}
		return nil
	}
	for o := range f.OptionHelp {
		if !slices.Contains(f.Options, o) {
			return fmt.Errorf("option_help %q: not an option", o)
		}
	}
	for o := range f.OptionLabels {
		if !slices.Contains(f.Options, o) {
			return fmt.Errorf("option_labels %q: not an option", o)
		}
	}
	return checkType(f.Type, f.Scale, f.Options)
}

func checkType(t FieldType, scale uint8, options []string) error {
	switch {
	case !slices.Contains(fieldTypes, t):
		return fmt.Errorf("unknown type %q", t)
	case t == Decimal && (scale == 0 || scale > 18):
		return fmt.Errorf("decimal scale %d, want 1..18", scale)
	case t == Enum && len(options) == 0:
		return errors.New("enum needs options")
	}
	return nil
}
