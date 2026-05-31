package listauthz

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/authz"
)

// recordingChecker captures the (subject, relation, object) of the last Check
// and returns a programmable verdict.
type recordingChecker struct {
	allow              bool
	err                error
	gotSubject         string
	gotRelation        string
	gotObject          string
}

func (c *recordingChecker) Check(_ context.Context, subjectID, relation, object string) (bool, error) {
	c.gotSubject, c.gotRelation, c.gotObject = subjectID, relation, object
	return c.allow, c.err
}

// TestAdapter_CanViewProject_Allowed — Check allowed → (true, nil); verifies the
// adapter targets relation "viewer" on object "project:<id>" (the EXACT
// relation/object the per-RPC gate uses; KAC-240 must not invent a new relation).
func TestAdapter_CanViewProject_Allowed(t *testing.T) {
	c := &recordingChecker{allow: true}
	a := New(c)

	ok, err := a.CanViewProject(context.Background(), "user:usr_x", "prj_1")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "user:usr_x", c.gotSubject)
	assert.Equal(t, "viewer", c.gotRelation)
	assert.Equal(t, "project:prj_1", c.gotObject)
}

// TestAdapter_CanViewProject_Denied — Check allowed=false → (false, nil) (caller
// returns empty list, fail-closed; NOT an error).
func TestAdapter_CanViewProject_Denied(t *testing.T) {
	a := New(&recordingChecker{allow: false})
	ok, err := a.CanViewProject(context.Background(), "user:usr_x", "prj_1")
	require.NoError(t, err)
	assert.False(t, ok)
}

// TestAdapter_CanViewProject_Error — Check error propagates (caller maps to
// Unavailable, fail-closed).
func TestAdapter_CanViewProject_Error(t *testing.T) {
	a := New(&recordingChecker{err: errors.New("iam down")})
	_, err := a.CanViewProject(context.Background(), "user:usr_x", "prj_1")
	require.Error(t, err)
}

// TestAdapter_CanViewProject_NilAdapter — nil adapter / nil checker → ErrUnavailable.
func TestAdapter_CanViewProject_NilAdapter(t *testing.T) {
	var a *Adapter
	_, err := a.CanViewProject(context.Background(), "user:usr_x", "prj_1")
	require.ErrorIs(t, err, authz.ErrUnavailable)

	a2 := New(nil)
	_, err = a2.CanViewProject(context.Background(), "user:usr_x", "prj_1")
	require.ErrorIs(t, err, authz.ErrUnavailable)
}

// TestAdapter_AsPort_NilWhenCheckerNil — AsPort returns nil-Port when checker nil.
func TestAdapter_AsPort_NilWhenCheckerNil(t *testing.T) {
	assert.Nil(t, AsPort(nil))
	assert.Nil(t, AsPort(New(nil)))
}

// TestAdapter_AsPort_NonNil — AsPort returns a usable Port when checker is set.
func TestAdapter_AsPort_NonNil(t *testing.T) {
	p := AsPort(New(&recordingChecker{allow: true}))
	require.NotNil(t, p)
	ok, err := p.CanViewProject(context.Background(), "user:usr_x", "prj_1")
	require.NoError(t, err)
	assert.True(t, ok)
}

// TestNewProjectChecker — returns nil-Port for nil checker, usable Port otherwise.
func TestNewProjectChecker(t *testing.T) {
	assert.Nil(t, NewProjectChecker(nil))

	p := NewProjectChecker(authz.CheckClientFunc(func(_ context.Context, _, _, _ string) (bool, error) {
		return true, nil
	}))
	require.NotNil(t, p)
	ok, err := p.CanViewProject(context.Background(), "user:usr_x", "prj_1")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestMapListFilterErr_PermissionDenied(t *testing.T) {
	err := MapListFilterErr(authz.ErrPermissionDenied)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.PermissionDenied, st.Code())
}

func TestMapListFilterErr_Unavailable(t *testing.T) {
	err := MapListFilterErr(authz.ErrUnavailable)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestMapListFilterErr_Nil(t *testing.T) {
	assert.NoError(t, MapListFilterErr(nil))
}
