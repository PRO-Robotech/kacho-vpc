package listauthz

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type item struct {
	ID   string
	Name string
}

func TestFilterByAllowedIDs_Exact(t *testing.T) {
	items := []*item{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	out := FilterByAllowedIDs(items, []string{"a", "c"}, func(i *item) string { return i.ID })
	assert.Len(t, out, 2)
	assert.Equal(t, "a", out[0].ID)
	assert.Equal(t, "c", out[1].ID)
}

func TestFilterByAllowedIDs_StableOrder(t *testing.T) {
	items := []*item{{ID: "z"}, {ID: "a"}, {ID: "m"}}
	out := FilterByAllowedIDs(items, []string{"a", "m", "z"}, func(i *item) string { return i.ID })
	assert.Len(t, out, 3)
	// Order preserved from input.
	assert.Equal(t, []string{"z", "a", "m"}, []string{out[0].ID, out[1].ID, out[2].ID})
}

func TestFilterByAllowedIDs_EmptyAllowed(t *testing.T) {
	items := []*item{{ID: "a"}, {ID: "b"}}
	out := FilterByAllowedIDs(items, nil, func(i *item) string { return i.ID })
	assert.Empty(t, out)
}

func TestFilterByAllowedIDs_EmptyItems(t *testing.T) {
	out := FilterByAllowedIDs[*item](nil, []string{"a"}, func(i *item) string { return i.ID })
	assert.Empty(t, out)
}

func TestFilterByAllowedIDs_AllAllowed(t *testing.T) {
	items := []*item{{ID: "a"}, {ID: "b"}}
	out := FilterByAllowedIDs(items, []string{"a", "b"}, func(i *item) string { return i.ID })
	assert.Len(t, out, 2)
}

func TestFilterByAllowedIDs_NoneAllowedFromNonEmpty(t *testing.T) {
	items := []*item{{ID: "a"}, {ID: "b"}}
	out := FilterByAllowedIDs(items, []string{"x", "y", "z"}, func(i *item) string { return i.ID })
	assert.Empty(t, out)
}

func TestFilterByAllowedIDs_ExtraAllowedIgnored(t *testing.T) {
	items := []*item{{ID: "a"}}
	out := FilterByAllowedIDs(items, []string{"a", "ghost-id"}, func(i *item) string { return i.ID })
	assert.Len(t, out, 1)
	assert.Equal(t, "a", out[0].ID)
}

func TestFilterByAllowedIDs_DuplicateAllowed(t *testing.T) {
	items := []*item{{ID: "a"}, {ID: "b"}}
	out := FilterByAllowedIDs(items, []string{"a", "a", "a"}, func(i *item) string { return i.ID })
	assert.Len(t, out, 1)
	assert.Equal(t, "a", out[0].ID)
}
