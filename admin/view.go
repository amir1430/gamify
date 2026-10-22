package admin

import (
	"context"
	"errors"
	"fmt"
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
)

var fieldTypes = []FieldType{Text, Int, Decimal, Money, Bool, Time, Enum, UserID, JSON}

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
}

// Filter is a query parameter of a view's list. Key is a column's, with the same Type; an Enum
// filter without Options uses the column's.
type Filter struct {
	Key     string    `json:"key"`
	Label   string    `json:"label"`
	Type    FieldType `json:"type"`
	Options []string  `json:"options,omitempty"`
}

// Field is an input of an action. Money and Decimal inputs are integers (minor units, scaled).
type Field struct {
	Key      string          `json:"key"`
	Label    string          `json:"label"`
	Type     FieldType       `json:"type"`
	Required bool            `json:"required"`
	Scale    uint8           `json:"scale,omitempty"`
	Currency ledger.Currency `json:"currency,omitempty"`
	Options  []string        `json:"options,omitempty"`
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

// Action is a button on a view's row. Run runs inside the admin's transaction: the console
// writes the audit row (action view.<view>.<action>, target <view>:<id>, Run's before and
// after, the reason) in tx and commits. An error or a panic rolls everything back. Run never
// moves money: the ledger isn't in tx. Change the game's row and let its reconciler do the rest.
type Action struct {
	Name, Title string
	Confirm     string // "" = no confirmation prompt
	Perm        Perm   // built-in or Config.Perms; the view's Perm is needed too
	Reason      bool   // a reason is required
	Fields      []Field
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
// name, no Run or an unknown permission.
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
	cols := map[string]Column{}
	v.Columns = slices.Clone(v.Columns)
	for i, c := range v.Columns {
		if c.Key == "" {
			return v, fmt.Errorf("column %d: key is required", i)
		}
		if _, ok := cols[c.Key]; ok {
			return v, fmt.Errorf("column %q: duplicate key", c.Key)
		}
		if err := checkType(c.Type, c.Scale, c.Options); err != nil {
			return v, fmt.Errorf("column %q: %w", c.Key, err)
		}
		if c.Type == Money && (c.CurrencyKey == "") == (c.Currency == "") {
			return v, fmt.Errorf("column %q: money needs one of CurrencyKey and Currency", c.Key)
		}
		if c.Label == "" {
			c.Label = c.Key
		}
		c.Options = slices.Clone(c.Options)
		v.Columns[i], cols[c.Key] = c, c
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
		case f.Type == JSON:
			return v, fmt.Errorf("filter %q: json columns can't be filtered", f.Key)
		case f.Key == "before" || f.Key == "limit":
			return v, fmt.Errorf("filter %q: the name is the list's paging parameter", f.Key)
		case filters[f.Key].Key != "":
			return v, fmt.Errorf("filter %q: duplicate key", f.Key)
		}
		if len(f.Options) == 0 {
			f.Options = c.Options
		}
		if f.Type == Enum && len(f.Options) == 0 {
			return v, fmt.Errorf("filter %q: enum needs options", f.Key)
		}
		if f.Label == "" {
			f.Label = c.Label
		}
		f.Options = slices.Clone(f.Options)
		v.Filters[i], filters[f.Key] = f, f
	}
	if v.UserFilter != "" {
		if f, ok := filters[v.UserFilter]; !ok || (f.Type != Int && f.Type != UserID) {
			return v, fmt.Errorf("user_filter %q: not an int or user_id filter", v.UserFilter)
		}
	}
	v.Actions = slices.Clone(v.Actions)
	seen := map[string]bool{}
	for i, a := range v.Actions {
		if !viewName.MatchString(a.Name) {
			return v, fmt.Errorf("action %q: name must match %s", a.Name, viewName)
		}
		if seen[a.Name] {
			return v, fmt.Errorf("action %q: duplicate name", a.Name)
		}
		seen[a.Name] = true
		if a.Title == "" {
			return v, fmt.Errorf("action %q: title is required", a.Name)
		}
		if a.Run == nil {
			return v, fmt.Errorf("action %q: run is required", a.Name)
		}
		if !adm.knownPerm(a.Perm) {
			return v, fmt.Errorf("action %q: unknown perm %q", a.Name, a.Perm)
		}
		a.Fields = slices.Clone(a.Fields)
		keys := map[string]bool{}
		for j, f := range a.Fields {
			if f.Key == "" || keys[f.Key] {
				return v, fmt.Errorf("action %q: field %q: key empty or duplicate", a.Name, f.Key)
			}
			keys[f.Key] = true
			if err := checkType(f.Type, f.Scale, f.Options); err != nil {
				return v, fmt.Errorf("action %q: field %q: %w", a.Name, f.Key, err)
			}
			if f.Label == "" {
				f.Label = f.Key
			}
			f.Options = slices.Clone(f.Options)
			a.Fields[j] = f
		}
		v.Actions[i] = a
	}
	return v, nil
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
