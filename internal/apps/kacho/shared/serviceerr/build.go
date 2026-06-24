// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package serviceerr

import (
	"errors"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// InvalidArg — собирает gRPC InvalidArgument с BadRequest-details по одному полю.
func InvalidArg(field, desc string) error {
	st := status.New(codes.InvalidArgument, desc)
	br := &errdetails.BadRequest{
		FieldViolations: []*errdetails.BadRequest_FieldViolation{
			{Field: field, Description: desc},
		},
	}
	if withDetails, derr := st.WithDetails(br); derr == nil {
		return withDetails.Err()
	}
	return st.Err()
}

// FromValidation — единая точка трансляции доменной `*domain.ValidationError`
// в gRPC `InvalidArgument` с BadRequest-details. Domain-слой остается stdlib-only
// и не тянет corelib/errors+grpc (domain = только stdlib + kacho-proto), поэтому
// конвертация в transport-ошибку живет здесь, в shared-пакете рядом с use-case'ами,
// которые его и так импортируют.
//
// Wire-контракт: code=InvalidArgument, message="invalid argument", details — один
// BadRequest с FieldViolation'ами в исходном порядке.
//
// nil → nil; не-ValidationError (например уже готовый gRPC-status) — passthrough.
func FromValidation(err error) error {
	if err == nil {
		return nil
	}
	var ve *domain.ValidationError
	if !errors.As(err, &ve) {
		return err
	}
	st := status.New(codes.InvalidArgument, "invalid argument")
	if len(ve.Violations) > 0 {
		fvs := make([]*errdetails.BadRequest_FieldViolation, 0, len(ve.Violations))
		for _, v := range ve.Violations {
			fvs = append(fvs, &errdetails.BadRequest_FieldViolation{Field: v.Field, Description: v.Msg})
		}
		if withDetails, derr := st.WithDetails(&errdetails.BadRequest{FieldViolations: fvs}); derr == nil {
			st = withDetails
		}
	}
	return st.Err()
}
