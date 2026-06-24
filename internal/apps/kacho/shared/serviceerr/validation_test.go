// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package serviceerr_test

import (
	"testing"

	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// TestFromValidation_ByteIdenticalToCoreErrors — трансляция доменной
// `*domain.ValidationError` в gRPC InvalidArgument должна быть байт-в-байт
// совместима с `coreerrors.InvalidArgument().AddFieldViolation(...).Err()`
// (тот же code, message, BadRequest-details в том же порядке). Так внешний
// wire-контракт остается неизменным после декаплинга domain от corelib/errors.
func TestFromValidation_ByteIdenticalToCoreErrors(t *testing.T) {
	domErr := &domain.ValidationError{Violations: []domain.FieldViolation{
		{Field: "name", Msg: "bad name"},
	}}

	got := serviceerr.FromValidation(domErr)
	want := coreerrors.InvalidArgument().AddFieldViolation("name", "bad name").Err()

	gotSt, ok := status.FromError(got)
	require.True(t, ok)
	wantSt, _ := status.FromError(want)

	assert.Equal(t, codes.InvalidArgument, gotSt.Code())
	assert.Equal(t, wantSt.Message(), gotSt.Message())

	// proto-эквивалентность всего status (включая BadRequest-details).
	assert.True(t, proto.Equal(gotSt.Proto(), wantSt.Proto()),
		"FromValidation must be byte-identical to coreerrors builder; got=%v want=%v",
		gotSt.Proto(), wantSt.Proto())

	// Явно: details содержит ровно одно BadRequest с нашим violation.
	det := gotSt.Details()
	require.Len(t, det, 1)
	br, isBR := det[0].(*errdetails.BadRequest)
	require.True(t, isBR)
	require.Len(t, br.FieldViolations, 1)
	assert.Equal(t, "name", br.FieldViolations[0].Field)
	assert.Equal(t, "bad name", br.FieldViolations[0].Description)
}

// TestFromValidation_Passthrough — nil → nil; не-ValidationError проходит как есть.
func TestFromValidation_Passthrough(t *testing.T) {
	assert.NoError(t, serviceerr.FromValidation(nil))

	other := status.Error(codes.NotFound, "x")
	assert.Equal(t, other, serviceerr.FromValidation(other))
}
